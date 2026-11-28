package main

import (
	"runtime"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/settings"
)

// streamView keeps the video stage and the controls within the window; all
// network operations remain in the existing session lifecycle.
func (a *app) streamView(c *ui.Context) {
	width, height := c.Size()
	pad := float32(16)
	if width < 700 || height < 500 {
		pad = 8
	}
	ui.Column(c).Fill().Background(dashBackground).Padding(pad).Gap(6).Children(func() {
		a.showSystemPrompt(c)
		if !a.controlsHidden {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "XSTREAMING").FontSize(16).Bold().TextColor(dashGreen)
				ui.Text(c, a.tr("Streaming session")).FontSize(12).TextColor(dashMuted).Grow(1)
				if ui.Button(c.Key("stop"), a.tr("Stop stream")).Clicked() {
					a.stopStream(false)
				}
			})
		}
		if a.frame != nil {
			ui.Image(c, a.frame).FillWidth().Grow(1).Fit(videoFit(a.prefs.VideoFit))
		} else {
			ui.Column(c).FillWidth().Grow(1).Background(ui.Hex("#151e1b")).Radius(12).Center().Gap(5).Children(func() {
				ui.Text(c, a.tr("Connecting your stream")).FontSize(20).Bold()
				if a.status != "" && a.status != "Streaming video." {
					ui.Text(c, a.tr(a.status)).FontSize(13).TextColor(dashMuted)
				}
			})
		}
		if a.controlsHidden {
			if a.showPerformance {
				a.performanceMetrics(c)
			}
			for _, warning := range []string{a.wakeWarning, a.micStatus, a.inputWarning} {
				if warning != "" {
					ui.Text(c, a.tr(warning)).FontSize(12).TextColor(dashMuted)
				}
			}
			if ui.Button(c.Key("show-stream-controls"), a.tr("Show controls")).Clicked() {
				a.controlsHidden = false
			}
			a.captureKeyboard(c, false)
			return
		}
		if a.frame != nil && a.status != "" && a.status != "Streaming video." {
			ui.Text(c, a.tr(a.status)).FontSize(12).TextColor(dashMuted)
		}
		for _, warning := range []string{a.wakeWarning, a.inputWarning, a.micStatus, a.textStatus} {
			if warning != "" {
				ui.Text(c, a.tr(warning)).FontSize(12).TextColor(dashMuted)
			}
		}
		if a.showPerformance {
			a.performanceMetrics(c)
		}
		ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
			label := "Full screen"
			if a.fullscreen {
				label = "Exit full screen"
			}
			if ui.Button(c.Key("fullscreen"), a.tr(label)).Clicked() && a.win != nil {
				a.win.ToggleFullScreen()
			}
			if ui.Button(c.Key("video-fit"), a.tr(a.prefs.VideoFit)).Clicked() {
				switch a.prefs.VideoFit {
				case "Fit":
					a.prefs.VideoFit = "Crop"
				case "Crop":
					a.prefs.VideoFit = "Stretch"
				default:
					a.prefs.VideoFit = "Fit"
				}
				if a.settingsPath != "" {
					if err := settings.Save(a.settingsPath, a.prefs); err != nil {
						a.status = "Unable to save settings: " + err.Error()
					}
				}
			}
			performanceLabel := "Show performance"
			if a.showPerformance {
				performanceLabel = "Hide performance"
			}
			if ui.Button(c.Key("performance"), a.tr(performanceLabel)).Clicked() {
				a.showPerformance = !a.showPerformance
			}
			if ui.Button(c.Key("hide-stream-controls"), a.tr("Hide controls")).Clicked() {
				a.controlsHidden = true
			}
			if a.streamHomeID != "" && a.streamDone != nil && a.streamPowerOff != nil && a.accountCtx != nil && a.credentials != nil {
				if ui.Button(c.Key("stop-and-power-off"), a.tr("Disconnect and power off")).Clicked() {
					a.stopStream(true)
				}
			}
		})
		ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
			if microphoneSupported(runtime.GOOS) && a.micSession != nil && (a.streamConnected || a.micCancel != nil) {
				label := "Enable microphone"
				if a.micCancel != nil {
					label = "Disable microphone"
				}
				if ui.Button(c.Key("microphone"), a.tr(label)).Disabled(a.micStopping).Clicked() {
					if a.micCancel == nil {
						a.startMicrophone()
					} else {
						a.micStopping = true
						a.micStatus = "Stopping microphone…"
						a.micCancel()
					}
				}
			}
			if a.streamConnected && a.inputReady {
				if ui.Button(c.Key("press-nexus"), a.tr("Press Xbox button")).Clicked() {
					a.keyboard.PressNexus(150 * time.Millisecond)
				}
				if a.streamHomeID != "" && ui.Button(c.Key("hold-nexus"), a.tr("Hold Xbox button")).Clicked() {
					a.keyboard.PressNexus(time.Second)
				}
			}
			label := "Enable keyboard controller"
			if a.keyboardEnabled {
				label = "Disable keyboard controller"
			}
			if ui.Button(c.Key("keyboard"), a.tr(label)).Clicked() {
				a.keyboardEnabled = !a.keyboardEnabled
			}
		})
		if a.streamHomeID != "" && a.streamConnected && !a.keyboardEnabled && a.streamCtx != nil && a.credentials != nil {
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				w := float32(320)
				if width < 500 {
					w = max(120, width-150)
				}
				ui.TextInput(c.Key("console-text"), &a.consoleText).Placeholder(a.tr("Text for console")).Label(a.tr("Text for console")).Width(w)
				if ui.Button(c.Key("send-console-text"), a.tr("Send text")).Disabled(a.textSending || a.consoleText == "").Clicked() {
					text, ctx, id, credentials := a.consoleText, a.streamCtx, a.streamHomeID, a.credentials
					a.textSending = true
					a.textStatus = "Sending text…"
					go a.sendConsoleText(ctx, credentials, id, text)
				}
			})
		}
		a.captureKeyboard(c, true)
	})
}
