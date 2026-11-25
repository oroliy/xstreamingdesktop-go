// Package settings persists non-secret preview preferences.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Settings struct {
	Resolution       string  `json:"resolution"`
	UILanguage       string  `json:"ui_language"`
	Language         string  `json:"language"`
	Market           string  `json:"market"`
	CloudRegion      string  `json:"cloud_region"`
	VideoFit         string  `json:"video_fit"`
	MicrophoneSource string  `json:"microphone_source"`
	Volume           float64 `json:"volume"`
	StickDeadzone    float64 `json:"stick_deadzone"`
	PollingRate      string  `json:"polling_rate"`
	PreferIPv6       bool    `json:"prefer_ipv6"`
	WakeBeforeStream bool    `json:"wake_before_stream"`
	Vibration        bool    `json:"vibration"`
}

// GameLanguages lists locales sent to the streaming and catalog services.
func GameLanguages() []string {
	return []string{"ar-SA", "cs-CZ", "da-DK", "de-DE", "el-GR", "en-GB", "en-US", "es-ES", "es-MX", "fi-FI", "fr-FR", "he-IL", "hu-HU", "it-IT", "ja-JP", "ko-KR", "nb-NO", "nl-NL", "pl-PL", "pt-BR", "pt-PT", "ru-RU", "sk-SK", "sv-SE", "tr-TR", "zh-CN", "zh-TW"}
}

// CatalogMarkets controls product metadata; it does not change the streaming region.
func CatalogMarkets() []string {
	return []string{"AU", "AT", "BE", "BR", "CA", "CH", "CN", "CZ", "DE", "DK", "ES", "FI", "FR", "GB", "HK", "IE", "IT", "JP", "KR", "MX", "NL", "NO", "NZ", "PL", "PT", "SE", "SG", "TW", "US"}
}

func Defaults() Settings {
	return Settings{Resolution: "1080p", UILanguage: "English", Language: "en-US", Market: "US", CloudRegion: "Automatic", VideoFit: "Fit", MicrophoneSource: "default", Volume: 100, StickDeadzone: 10, PollingRate: "60 Hz", Vibration: true}
}
func (s Settings) Validate() error {
	if s.MicrophoneSource == "" || len(s.MicrophoneSource) > 256 || strings.TrimSpace(s.MicrophoneSource) != s.MicrophoneSource || strings.ContainsAny(s.MicrophoneSource, "\x00\r\n") {
		return errors.New("invalid microphone source")
	}
	if s.VideoFit != "Fit" && s.VideoFit != "Crop" && s.VideoFit != "Stretch" {
		return errors.New("invalid video fit")
	}
	if s.UILanguage != "English" && s.UILanguage != "简体中文" {
		return errors.New("invalid interface language")
	}
	if math.IsNaN(s.Volume) || math.IsInf(s.Volume, 0) || s.Volume < 0 || s.Volume > 100 {
		return errors.New("invalid volume")
	}
	if math.IsNaN(s.StickDeadzone) || math.IsInf(s.StickDeadzone, 0) || s.StickDeadzone < 0 || s.StickDeadzone > 50 {
		return errors.New("invalid stick dead zone")
	}
	switch s.PollingRate {
	case "60 Hz", "125 Hz", "250 Hz":
	default:
		return errors.New("invalid controller polling rate")
	}
	switch s.Resolution {
	case "720p", "1080p", "1080p HQ":
	default:
		return errors.New("invalid resolution")
	}
	if !slices.Contains(GameLanguages(), s.Language) {
		return errors.New("invalid language")
	}
	if !slices.Contains(CatalogMarkets(), s.Market) {
		return errors.New("invalid market")
	}
	if s.CloudRegion == "" || len(s.CloudRegion) > 128 || strings.TrimSpace(s.CloudRegion) != s.CloudRegion || strings.ContainsAny(s.CloudRegion, "\r\n") {
		return errors.New("invalid cloud region")
	}
	return nil
}

// PollingRateHz is the controller sampling frequency for a new stream.
func (s Settings) PollingRateHz() int {
	switch s.PollingRate {
	case "125 Hz":
		return 125
	case "250 Hz":
		return 250
	default:
		return 60
	}
}
func (s Settings) ResolutionCode() int {
	switch s.Resolution {
	case "720p":
		return 720
	case "1080p HQ":
		return 1081
	default:
		return 1080
	}
}
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "xstreaming-go", "settings.json"), nil
}
func Load(path string) (Settings, error) {
	s := Defaults()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return Defaults(), err
	}
	if err = s.Validate(); err != nil {
		return Defaults(), err
	}
	return s, nil
}
func Save(path string, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
