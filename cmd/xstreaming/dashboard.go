package main

import (
	"context"
	"fmt"
	"runtime"

	"github.com/egoist/mygo/ui"
	"github.com/oroliy/xstreamingdesktop-go/settings"
)

var dashBackground = ui.Hex("#0d1113")
var dashPanel = ui.Hex("#171e20")
var dashBorder = ui.Hex("#293536")
var dashMuted = ui.Hex("#9aa9a5")
var dashGreen = ui.Hex("#a9e776")

func (a *app) view(c *ui.Context) {
	theme := ui.DarkTheme()
	theme.Background = dashBackground
	theme.Surface = ui.Hex("#24302f")
	theme.SurfaceHover = ui.Hex("#344642")
	theme.SurfacePressed = ui.Hex("#3b5148")
	theme.Border = ui.Hex("#354541")
	theme.Text = ui.Hex("#eef6ef")
	theme.TextMuted = dashMuted
	theme.Accent = dashGreen
	theme.AccentText = ui.Hex("#10200e")
	theme.InactiveAccent = dashGreen
	theme.Radius = 8
	c.SetTheme(theme)
	if a.streamCancel != nil {
		a.streamView(c)
		return
	}
	if a.systemPrompt != nil || a.frame != nil {
		a.legacyView(c)
		return
	}
	a.dashboard(c)
}

func dashboardCard(c *ui.Context, content func()) {
	ui.Column(c).FillWidth().Background(dashPanel).Border(1, dashBorder).Radius(16).Padding(20).Gap(12).Children(content)
}

func dashboardHeading(c *ui.Context, title, caption string) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(23).Bold()
		ui.Text(c, caption).FontSize(13).TextColor(dashMuted)
	})
}

func (a *app) dashboard(c *ui.Context) {
	width, _ := c.Size()
	compact := width < 700
	pad := float32(30)
	if compact {
		pad = 16
	}
	ui.Column(c).Fill().Background(dashBackground).Children(func() {
		ui.Row(c).FillWidth().Padding(pad, pad, 16, pad).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "XSTREAMING").FontSize(19).Bold().TextColor(dashGreen)
				ui.Text(c, a.tr("YOUR PLAY, ANYWHERE")).FontSize(10).TextColor(dashMuted)
			})
			if a.credentials != nil && !a.busy {
				if ui.Button(c.Key("signout"), a.tr("Sign out")).Clicked() {
					a.busy = true
					a.status = "Signing out…"
					a.accountCancel()
					credentials := a.credentials
					go func() {
						err := credentials.Logout()
						a.update(func() {
							a.busy = false
							a.client = nil
							a.cloud = nil
							a.credentials = nil
							a.consoles = nil
							a.titles = nil
							a.cloudStatus = ""
							a.cloudRegions = nil
							a.wakeWarning = ""
							a.status = "Signed out."
							if err != nil {
								a.status = "Signed out locally; unable to remove saved login: " + err.Error()
							}
						})
					}()
				}
			}
		})
		ui.Row(c).FillWidth().Padding(0, pad, 12, pad).Gap(8).Wrap().Children(func() {
			for _, tab := range []struct{ id, label string }{{"", "Consoles"}, {"cloud", "Cloud games"}, {"settings", "Settings"}} {
				if a.page == tab.id {
					if ui.PrimaryButton(c.Key("nav-"+tab.id), a.tr(tab.label)).Clicked() {
						a.page = tab.id
					}
				} else {
					if ui.Button(c.Key("nav-"+tab.id), a.tr(tab.label)).Clicked() {
						a.page = tab.id
					}
				}
			}
		})
		ui.Scroll(c.Key("library-"+a.page)).FillWidth().Grow(1).Padding(8, pad, 30, pad).Gap(18).Children(func() {
			if a.status != "" || a.wakeWarning != "" || a.inputWarning != "" {
				ui.Column(c).FillWidth().Background(ui.Hex("#202c28")).Border(1, ui.Hex("#354a3b")).Radius(10).Padding(12, 16).Gap(4).Children(func() {
					if a.status != "" {
						ui.Text(c, a.tr(a.status)).FontSize(13)
					}
					if a.wakeWarning != "" {
						ui.Text(c, a.tr(a.wakeWarning)).FontSize(13)
					}
					if a.inputWarning != "" {
						ui.Text(c, a.tr(a.inputWarning)).FontSize(13)
					}
				})
			}
			if a.code != "" {

				ui.Text(c, a.tr("Visit: ")+a.uri)
				ui.Text(c, a.tr("Code: ")+a.code).FontSize(24).Bold()
				if ui.Button(c.Key("copy-code"), a.tr("Copy sign-in code")).Clicked() {
					c.WriteClipboard(a.code)
				}
				if verificationURLAllowed(a.uri) {
					if ui.Button(c.Key("open-verification"), a.tr("Open Microsoft sign-in page")).Clicked() {
						address := a.uri
						c.OpenURLThen(address, func(err error) {
							if err != nil && a.uri == address {
								a.status = "Unable to open browser; enter the address manually."
							}
						})
					}
				}
			}
			if a.busy && a.credentials == nil && a.accountCancel != nil && !a.cancelingLogin {
				if ui.Button(c.Key("cancel-login"), a.tr("Cancel sign-in")).Clicked() {
					a.cancelingLogin = true
					a.accountCancel()
					a.code = ""
					a.uri = ""
					a.status = "Canceling sign-in…"
				}
			}
			if a.page == "" && !a.busy && a.code == "" && a.credentials == nil && a.controllerTestCancel == nil {
				dashboardCard(c, func() {
					dashboardHeading(c, a.tr("Welcome to XStreaming"), a.tr("Sign in to connect your console and discover cloud games."))
					if ui.PrimaryButton(c.Key("signin"), a.tr("Sign in with Microsoft")).Clicked() {
						a.busy = true
						a.status = "Requesting sign-in code…"
						ctx, cancel := context.WithCancel(a.ctx)
						a.accountCtx = ctx
						a.accountCancel = cancel
						go a.signIn(ctx, a.prefs)
					}
				})
			}
			switch a.page {
			case "settings":
				a.dashboardSettings(c)
			case "cloud":
				a.dashboardCloud(c)
			default:
				a.dashboardConsoles(c)
			}
		})
	})
}

func (a *app) dashboardConsoles(c *ui.Context) {
	dashboardHeading(c, a.tr("Consoles"), a.tr("Your Xbox, ready when you are."))
	dashboardCard(c, func() {
		ui.Row(c).FillWidth().Gap(12).Wrap().AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, a.tr("Your consoles")).FontSize(17).Bold()
				ui.Text(c, a.tr("Connect to a console on your account.")).FontSize(12).TextColor(dashMuted)
			})
			if a.credentials != nil && !a.busy {
				if ui.Button(c.Key("refresh-consoles"), a.tr("Refresh consoles")).Clicked() {
					a.busy = true
					a.status = "Refreshing consoles…"
					go a.refreshConsoles(a.accountCtx, a.credentials, a.client)
				}
			}
		})
		if len(a.consoles) == 0 {
			ui.Text(c, a.tr("No consoles to show yet.")).TextColor(dashMuted)
		}
		for _, console := range a.consoles {
			console := console
			ui.Column(c).FillWidth().Background(ui.Hex("#202b29")).Radius(12).Padding(16).Gap(12).Children(func() {
				ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(42, 42).Radius(12).Center().Background(ui.Hex("#344b36")).Children(func() { ui.Text(c, "X").FontSize(20).Bold().TextColor(dashGreen) })
					ui.Column(c).Grow(1).Gap(3).Children(func() {
						ui.Text(c, console.Name).FontSize(18).Bold()
						ui.Text(c, console.ConsoleType+"  ·  "+a.tr(console.PowerState)).FontSize(12).TextColor(dashMuted)
					})
				})
				if !a.busy && console.ServerID != "" {
					ui.Row(c).Gap(8).Wrap().Children(func() {
						if ui.PrimaryButton(c.Key(console.ServerID), a.tr("Start stream")).Clicked() {
							ctx, cancel := context.WithCancel(a.ctx)
							a.streamCancel = cancel
							a.controlsHidden = false
							a.streamConnected = false
							a.consoleText, a.textStatus, a.textSending = "", "", false
							a.busy = true
							a.status = "Starting stream…"
							a.wakeWarning = ""
							a.streamHomeID = console.ServerID
							wake := a.prefs.WakeBeforeStream && console.PowerState == "ConnectedStandby"
							a.startStreamAsync(ctx, a.client, a.credentials, console.ServerID, a.prefs, wake)
						}
						if ui.Button(c.Key("wake-"+console.ServerID), a.tr("Wake console")).Clicked() {
							a.busy = true
							a.status = "Sending wake request…"
							go a.wakeConsole(a.accountCtx, a.credentials, console.ServerID)
						}
						if ui.Button(c.Key("power-off-"+console.ServerID), a.tr("Power off console")).Clicked() {
							a.busy = true
							a.status = "Sending power-off request…"
							go a.powerOffConsole(a.accountCtx, a.credentials, console.ServerID)
						}
					})
				}
			})
		}
	})
	dashboardCard(c, func() {
		dashboardHeading(c, a.tr("Controller"), a.tr("Check your controller before playing."))
		a.controllerTestView(c)
	})
	ui.Text(c, a.tr("Experimental preview — controller and playback support are unverified.")).FontSize(12).TextColor(dashMuted)
}

func (a *app) dashboardSettings(c *ui.Context) {
	dashboardHeading(c, a.tr("Settings"), a.tr("Make your streaming experience your own."))
	dashboardCard(c, func() {
		if microphoneSourceSettingsSupported(runtime.GOOS) {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, a.tr("Microphone input"))
				options, names, selected := microphoneSourceOptions(a.micSources, a.micSourceDraft)
				if a.micSourceChoice == "" {
					a.micSourceChoice = selected
				}
				if ui.Select(c.Key("microphone-sources"), &a.micSourceChoice, options).Label(a.tr("Available microphone inputs")).Width(200).Changed() {
					a.micSourceDraft = names[a.micSourceChoice]
				}
			})
			if ui.Button(c.Key("refresh-microphone-sources"), a.tr("Refresh microphone inputs")).Width(200).Disabled(a.micSourcesLoading).Clicked() {
				a.refreshMicrophoneSources()
			}
			if a.micSourcesStatus != "" {
				ui.Text(c, a.tr(a.micSourcesStatus))
			}
			if runtime.GOOS == "windows" {
				ui.Text(c, a.tr("Choose a microphone input before enabling capture."))
			}
			ui.Row(c).Gap(8).Children(func() {
				label := "PulseAudio input name"
				placeholder := "default"
				if runtime.GOOS == "windows" {
					label = "DirectShow input name"
					placeholder = a.tr("Select a device")
				}
				if ui.TextInput(c.Key("microphone-source"), &a.micSourceDraft).Label(a.tr(label)).Placeholder(placeholder).MaxLength(256).Width(300).Changed() {
					_, _, a.micSourceChoice = microphoneSourceOptions(a.micSources, a.micSourceDraft)
				}
				if ui.Button(c.Key("save-microphone-source"), a.tr("Save microphone input")).Clicked() {
					a.saveMicrophoneSource()
				}
			})
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.tr("Interface language"))
			if ui.Select(c.Key("ui-language"), &a.prefs.UILanguage, []string{"English", "简体中文"}).Changed() && a.settingsPath != "" {
				if err := settings.Save(a.settingsPath, a.prefs); err != nil {
					a.status = "Unable to save settings: " + err.Error()
				}
			}
		})
		var changed, languageChanged, marketChanged bool
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.tr("Resolution"))
			changed = ui.Select(c.Key("resolution"), &a.prefs.Resolution, []string{"720p", "1080p", "1080p HQ"}).Width(140).Changed()
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.tr("Language"))
			languageChanged = ui.Select(c.Key("language"), &a.prefs.Language, settings.GameLanguages()).Width(140).Changed()
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.tr("Market"))
			marketChanged = ui.Select(c.Key("market"), &a.prefs.Market, settings.CatalogMarkets()).Width(140).Changed()
		})
		if (languageChanged || marketChanged) && a.credentials != nil && a.accountCtx != nil && a.accountCtx.Err() == nil && a.streamCancel == nil {
			a.startCloudLoad(a.accountCtx, a.credentials, a.prefs)
		}
		if (changed || languageChanged || marketChanged) && a.settingsPath != "" {
			if err := settings.Save(a.settingsPath, a.prefs); err != nil {
				a.status = "Unable to save settings: " + err.Error()
			}
		}
		if a.credentials != nil {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, a.tr("Cloud region"))
				if ui.Select(c.Key("cloud-region"), &a.prefs.CloudRegion, cloudRegionOptions(a.cloudRegions, a.prefs.CloudRegion)).Changed() {
					if a.settingsPath != "" {
						if err := settings.Save(a.settingsPath, a.prefs); err != nil {
							a.status = "Unable to save settings: " + err.Error()
						}
					}
					if a.accountCtx != nil && a.accountCtx.Err() == nil {
						a.cloud = nil
						a.titles = nil
						a.startCloudLoad(a.accountCtx, a.credentials, a.prefs)
					}
				}
			})
		}
		if ui.Checkbox(c.Key("wake-before-stream"), &a.prefs.WakeBeforeStream, a.tr("Wake standby console before streaming")).Changed() && a.settingsPath != "" {
			if err := settings.Save(a.settingsPath, a.prefs); err != nil {
				a.status = "Unable to save settings: " + err.Error()
			}
		}
		if ui.Checkbox(c.Key("prefer-ipv6"), &a.prefs.PreferIPv6, a.tr("Prefer IPv6 candidates")).Changed() && a.settingsPath != "" {
			if err := settings.Save(a.settingsPath, a.prefs); err != nil {
				a.status = "Unable to save settings: " + err.Error()
			}
		}
		if runtime.GOOS == "windows" && ui.Checkbox(c.Key("vibration"), &a.prefs.Vibration, a.tr("Controller vibration (next stream)")).Changed() && a.settingsPath != "" {
			if err := settings.Save(a.settingsPath, a.prefs); err != nil {
				a.status = "Unable to save settings: " + err.Error()
			}
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.tr("Starting volume"))
			if ui.Slider(c.Key("volume"), &a.prefs.Volume, 0, 100).Label(a.tr("Starting volume")).Width(180).Changed() && a.settingsPath != "" {
				if err := settings.Save(a.settingsPath, a.prefs); err != nil {
					a.status = "Unable to save settings: " + err.Error()
				}
			}
			ui.Text(c, fmt.Sprintf("%.0f%%", a.prefs.Volume))
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.tr("Stick dead zone"))
			if ui.Slider(c.Key("stick-deadzone"), &a.prefs.StickDeadzone, 0, 50).Label(a.tr("Stick dead zone")).Width(180).Changed() && a.settingsPath != "" {
				if err := settings.Save(a.settingsPath, a.prefs); err != nil {
					a.status = "Unable to save settings: " + err.Error()
				}
			}
			ui.Text(c, fmt.Sprintf("%.0f%%", a.prefs.StickDeadzone))
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.tr("Controller polling rate"))
			if ui.Select(c.Key("polling-rate"), &a.prefs.PollingRate, []string{"60 Hz", "125 Hz", "250 Hz"}).Changed() && a.settingsPath != "" {
				if err := settings.Save(a.settingsPath, a.prefs); err != nil {
					a.status = "Unable to save settings: " + err.Error()
				}
			}
		})

	})
}

func (a *app) dashboardCloud(c *ui.Context) {
	dashboardHeading(c, a.tr("Cloud games"), a.tr("Find your next game."))
	dashboardCard(c, func() {
		if ui.TextInput(c.Key("search"), &a.filter).Placeholder(a.tr("Search games")).Label(a.tr("Search games")).Width(320).Changed() {
			a.cloudPage = 0
		}
		if a.credentials != nil && a.accountCtx != nil && !a.busy && a.streamCancel == nil {
			if ui.Button(c.Key("reload-cloud"), a.tr("Reload cloud catalog")).Width(180).Clicked() {
				a.startCloudLoad(a.accountCtx, a.credentials, a.prefs)
			}
		}
		if a.cloudStatus != "" {
			ui.Text(c, a.tr(a.cloudStatus))
		}
		matches := filterCloudTitles(a.titles, a.filter)
		const pageSize = 40
		lastPage := max(0, (len(matches)-1)/pageSize)
		a.cloudPage = min(max(a.cloudPage, 0), lastPage)
		start := a.cloudPage * pageSize
		end := min(start+pageSize, len(matches))
		if len(matches) > 0 {
			ui.Row(c).Gap(8).Children(func() {
				if a.cloudPage > 0 && ui.Button(c.Key("cloud-previous"), a.tr("Previous games")).Clicked() {
					a.cloudPage--
				}
				ui.Text(c, fmt.Sprintf(a.tr("Games %d–%d of %d"), start+1, end, len(matches)))
				if a.cloudPage < lastPage && ui.Button(c.Key("cloud-next"), a.tr("Next games")).Clicked() {
					a.cloudPage++
				}
			})
		} else if a.filter != "" {
			ui.Text(c, a.tr("No matching games."))
		}
		for index, title := range matches[start:end] {
			index, title := index, title
			ui.Row(c).FillWidth().Background(ui.Hex("#202b29")).Radius(9).Padding(10, 14).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title.Name).Grow(1)
				if !a.busy && a.streamCancel == nil && a.cloud != nil {
					if ui.Button(c.Key(fmt.Sprintf("cloud-%d-%s", start+index, title.TitleID)), a.tr("Play")).Clicked() {
						ctx, cancel := context.WithCancel(a.ctx)
						a.streamCancel = cancel
						a.controlsHidden = false
						a.streamConnected = false
						a.consoleText, a.textStatus, a.textSending = "", "", false
						a.busy = true
						a.status = "Starting cloud game…"
						a.wakeWarning = ""
						a.streamHomeID = ""
						a.startStreamAsync(ctx, a.cloud, a.credentials, title.TitleID, a.prefs, false)
					}
				}
			})
		}
	})
}
