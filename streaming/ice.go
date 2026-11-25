package streaming

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/pion/ice/v4"
)

type ICECandidate struct {
	Candidate     string `json:"candidate"`
	MessageType   string `json:"messageType"`
	SDPMLineIndex string `json:"sdpMLineIndex"`
	SDPMid        string `json:"sdpMid"`
}

// UnmarshalJSON accepts both browser numeric and service string m-line indexes.
func (c *ICECandidate) UnmarshalJSON(data []byte) error {
	var wire struct {
		Candidate   string          `json:"candidate"`
		MessageType string          `json:"messageType"`
		Index       json.RawMessage `json:"sdpMLineIndex"`
		Mid         string          `json:"sdpMid"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	index := "0"
	if len(wire.Index) > 0 && !bytes.Equal(wire.Index, []byte("null")) {
		if wire.Index[0] == '"' {
			if err := json.Unmarshal(wire.Index, &index); err != nil {
				return err
			}
		} else {
			index = string(wire.Index)
		}
		if _, err := strconv.ParseUint(index, 10, 16); err != nil {
			return fmt.Errorf("invalid ICE m-line index: %w", err)
		}
	}
	*c = ICECandidate{Candidate: wire.Candidate, MessageType: wire.MessageType, SDPMLineIndex: index, SDPMid: wire.Mid}
	return nil
}

// MarshalJSON sends the m-line index as a number, as browser ICE candidates do.
func (c ICECandidate) MarshalJSON() ([]byte, error) {
	index, err := strconv.ParseUint(c.SDPMLineIndex, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid ICE m-line index: %w", err)
	}
	return json.Marshal(struct {
		Candidate   string `json:"candidate"`
		MessageType string `json:"messageType,omitempty"`
		Index       uint16 `json:"sdpMLineIndex"`
		Mid         string `json:"sdpMid"`
	}{c.Candidate, c.MessageType, uint16(index), c.SDPMid})
}

type parsedCandidate struct {
	ip        netip.Addr
	candidate ICECandidate
	fields    []string
}

// NormalizeICE retains candidate transport and media identity while applying
// the original client's Teredo, IPv6 preference and priority rules.
func NormalizeICE(input []ICECandidate, preferIPv6 bool) ([]ICECandidate, error) {
	var parsed []parsedCandidate
	var firstErr error
	hadCandidate := false
	for _, item := range input {
		if item.Candidate == "a=end-of-candidates" || item.Candidate == "end-of-candidates" {
			continue
		}
		hadCandidate = true
		fields := strings.Fields(strings.TrimPrefix(item.Candidate, "a="))
		if len(fields) < 8 || !strings.HasPrefix(fields[0], "candidate:") || fields[6] != "typ" {
			if firstErr == nil {
				firstErr = fmt.Errorf("invalid ICE candidate")
			}
			continue
		}
		ip, err := netip.ParseAddr(fields[4])
		if err != nil {
			name := strings.ToLower(fields[4])
			if fields[7] != "host" || !validMDNSName(name) {
				if firstErr == nil {
					firstErr = fmt.Errorf("invalid ICE address: %w", err)
				}
				continue
			}
			fields[4] = name
		}
		port, err := strconv.ParseUint(fields[5], 10, 16)
		if err != nil || port == 0 {
			if firstErr == nil {
				firstErr = fmt.Errorf("invalid ICE port")
			}
			continue
		}
		if _, err := ice.UnmarshalCandidate(strings.Join(fields, " ")); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("invalid ICE candidate: %w", err)
			}
			continue
		}
		if item.SDPMLineIndex == "" {
			item.SDPMLineIndex = "0"
		}
		if item.SDPMid == "" && item.SDPMLineIndex == "0" {
			item.SDPMid = "0"
		}
		if ip.Is6() && strings.EqualFold(fields[2], "udp") {
			a := ip.As16()
			if a[0] == 0x20 && a[1] == 0x01 && a[2] == 0 && a[3] == 0 {
				v4 := netip.AddrFrom4([4]byte{^a[12], ^a[13], ^a[14], ^a[15]})
				teredoPort := uint16(^a[10])<<8 | uint16(^a[11])
				for _, syntheticPort := range []uint16{9002, teredoPort} {
					if syntheticPort == 0 {
						continue
					}
					synthetic := []string{fmt.Sprintf("candidate:td%d", len(parsed)), fields[1], "UDP", "1", v4.String(), strconv.Itoa(int(syntheticPort)), "typ", "host"}
					parsed = append(parsed, parsedCandidate{v4, item, synthetic})
				}
			}
		}
		parsed = append(parsed, parsedCandidate{ip, item, fields})
	}
	if hadCandidate && len(parsed) == 0 {
		return nil, firstErr
	}
	if preferIPv6 {
		sort.SliceStable(parsed, func(i, j int) bool { return parsed[i].ip.Is6() && !parsed[j].ip.Is6() })
	}
	result := make([]ICECandidate, 0, len(parsed)+1)
	for i, item := range parsed {
		priority := 1
		if i == 0 {
			priority = 2130706431
		}
		item.fields[3] = strconv.Itoa(priority)
		item.candidate.Candidate = "a=" + strings.Join(item.fields, " ")
		if item.candidate.MessageType == "" {
			item.candidate.MessageType = "iceCandidate"
		}
		result = append(result, item.candidate)
	}
	return append(result, ICECandidate{Candidate: "a=end-of-candidates", MessageType: "iceCandidate", SDPMLineIndex: "0", SDPMid: "0"}), nil
}

// Pion resolves single-label host candidates in the .local mDNS domain.
func validMDNSName(name string) bool {
	label, ok := strings.CutSuffix(name, ".local")
	if !ok || len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}
