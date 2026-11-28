package main

import "strings"

var chinese = map[string]string{
	"YOUR PLAY, ANYWHERE": "随时随地，畅玩游戏", "Welcome to XStreaming": "欢迎使用 XStreaming", "Sign in to connect your console and discover cloud games.": "登录后连接主机，探索云游戏。",
	"Your Xbox, ready when you are.": "连接你的 Xbox，随时开始游戏。", "Your consoles": "我的主机", "Connect to a console on your account.": "连接此账号下的 Xbox 主机。", "No consoles to show yet.": "暂无可显示的主机。",
	"Controller": "手柄", "Check your controller before playing.": "开始游戏前，先检查手柄状态。", "Make your streaming experience your own.": "按你的习惯设置串流体验。", "Find your next game.": "发现下一款想玩的游戏。",
	"Test controllers": "测试手柄", "Stop controller test": "结束手柄测试", "No controllers detected.": "未检测到手柄。", "Buttons: none": "按键：无", "Buttons: ": "按键：",
	"Test vibration": "测试震动", "Controller vibration test unavailable.": "无法测试手柄震动。",
	"If a connected controller is missing, check read access to /dev/input/event*.": "如果已连接的手柄未显示，请检查对 /dev/input/event* 的读取权限。",
	"Text input": "文本输入", "Submit": "提交", "Cancel": "取消",
	"Experimental preview — controller and playback support are unverified.": "实验预览版：手柄和播放功能尚未经实机验证。",
	"Full screen": "全屏", "Exit full screen": "退出全屏", "Stop stream": "停止串流", "Disconnect and power off": "断开连接并关机", "Cancel sign-in": "取消登录",
	"Press Xbox button": "短按 Xbox 按钮", "Hold Xbox button": "长按 Xbox 按钮",
	"Video display": "画面显示", "Fit": "适应", "Crop": "裁剪填满", "Stretch": "拉伸",
	"Show performance": "显示性能数据", "Hide performance": "隐藏性能数据", "Video data: %.2f Mb/s": "视频数据：%.2f Mb/s",
	"Hide controls": "收起控制栏", "Show controls": "显示控制栏",
	"Enable microphone": "启用麦克风", "Disable microphone": "关闭麦克风", "Starting microphone…": "正在启动麦克风…", "Microphone on.": "麦克风已开启。", "Stopping microphone…": "正在关闭麦克风…",
	"PulseAudio input name": "PulseAudio 输入设备名", "Save microphone input": "保存麦克风设备", "Microphone input saved.": "麦克风设备已保存。", "Invalid microphone input name.": "麦克风设备名无效。",
	"DirectShow input name": "DirectShow 输入设备名", "Select a device": "请选择设备", "Choose a microphone input before enabling capture.": "启用麦克风前请先选择输入设备。", "Choose a Windows microphone in settings.": "请先在设置中选择 Windows 麦克风。",
	"Microphone input": "麦克风输入", "Available microphone inputs": "可用麦克风输入", "Refresh microphone inputs": "刷新麦克风输入", "Loading microphone inputs…": "正在获取麦克风输入…", "No microphone inputs found.": "未找到麦克风输入。", "Microphone inputs refreshed.": "麦克风输入已刷新。",
	"Connection did not recover; please retry.": "连接未能恢复，请重试。",
	"Controller input interrupted; retrying…":   "手柄输入中断，正在重试…",
	"Open Microsoft sign-in page":               "打开微软登录页面", "Copy sign-in code": "复制登录代码", "Enable keyboard controller": "启用键盘手柄", "Disable keyboard controller": "停用键盘手柄",
	"WASD: left stick · TFGH: right stick · IJKL: buttons · arrows: D-pad · Esc: disable": "WASD：左摇杆 · TFGH：右摇杆 · IJKL：面键 · 方向键：方向键 · Esc：退出",
	"Sign-in expired; please sign in again.":                                              "登录信息已失效，请重新登录。", "Sign in with Microsoft": "使用微软账号登录", "Sign out": "退出登录", "Consoles": "主机", "Refresh consoles": "刷新主机", "Wake console": "唤醒主机", "Power off console": "关闭主机", "Start stream": "开始串流",
	"Settings": "设置", "Cloud region": "云串流区域", "Resolution": "分辨率", "Language": "游戏语言", "Market": "目录市场", "Interface language": "界面语言", "Prefer IPv6 candidates": "优先使用 IPv6 候选地址", "Wake standby console before streaming": "串流前唤醒待机主机", "Starting volume": "启动音量", "Stick dead zone": "摇杆死区", "Controller polling rate": "手柄轮询频率",
	"Controller vibration (next stream)": "手柄震动（下次串流生效）",
	"Decoded video: %.1f FPS":            "视频解码：%.1f 帧/秒", "Keyboard controller": "键盘手柄", "Text for console": "发送到主机的文本", "Send text": "发送文本", "Sending text…": "正在发送文本…", "Text sent.": "文本已发送。", "Controller input is still connecting.": "手柄输入仍在连接中。", "On": "开机", "Off": "关机", "ConnectedStandby": "待机",
	"Cloud games": "云游戏", "Previous games": "上一页", "Next games": "下一页", "Games %d–%d of %d": "游戏 %d–%d，共 %d 个", "No matching games.": "没有匹配的游戏。", "Search games": "搜索游戏", "Reload cloud catalog": "重新加载云游戏目录", "Play": "开始游戏", "Visit: ": "访问：", "Code: ": "代码：",
	"Stopping stream…": "正在停止串流…", "Stopping stream before power-off…": "正在停止串流，随后关闭主机…", "Canceling sign-in…": "正在取消登录…", "Sign-in canceled.": "登录已取消。", "Requesting sign-in code…": "正在获取登录代码…", "Complete sign-in in your browser.": "请在浏览器中完成登录。", "Connecting to Xbox…": "正在连接 Xbox…", "Restoring sign-in…": "正在恢复登录…", "Signing out…": "正在退出登录…", "Signed out.": "已退出登录。",
	"Refreshing consoles…": "正在刷新主机…", "Sending wake request…": "正在发送唤醒请求…", "Sending power-off request…": "正在发送关机请求…", "Power-off request sent. Refresh the console list to check its state.": "关机请求已发送，可刷新主机列表查看状态。", "Wake request sent. The console may take a moment to start.": "唤醒请求已发送，主机可能需要一些时间启动。", "Starting stream…": "正在启动串流…", "Starting cloud game…": "正在启动云游戏…",
	"Preparing stream…": "正在准备串流…", "Waiting for available capacity…": "正在等待可用资源…", "Authorizing connection…": "正在授权连接…", "Negotiating connection…": "正在协商连接…", "Negotiation complete; waiting for connection…": "协商完成，正在等待连接…", "Connected; waiting for video…": "已连接，正在等待视频…", "Streaming video.": "正在播放视频。", "Connection interrupted…": "连接中断…", "Connection timed out; please retry.": "连接超时，请重试。", "Connection failed; cleaning up…": "连接失败，正在清理…", "Connection failed; please retry.": "连接失败，请重试。", "Stream stopped.": "串流已停止。",
	"Loading cloud games…": "正在加载云游戏…", "Cloud games unavailable for this account or region.": "当前账号或地区无法使用云游戏。", "Invalid cloud service region.": "云服务区域无效。", "Unable to load cloud games.": "无法加载云游戏。", "Home streaming unavailable; checking cloud games.": "主机串流不可用，正在检查云游戏。", "System credential store unavailable; sign in manually.": "系统凭据库不可用，请手动登录。", "Unable to save renewed login; sign in may be required next time.": "无法保存更新的登录信息，下次可能需要重新登录。", "Unable to open browser; enter the address manually.": "无法打开浏览器，请手动输入地址。",
}
var chinesePrefixes = []struct{ en, zh string }{
	{"Unable to list microphone inputs: ", "无法获取麦克风输入："},
	{"Microphone failed: ", "麦克风启动或采集失败："},
	{"Connection did not recover; remote session cleanup failed: ", "连接未能恢复，且远程会话清理失败："},
	{"ffmpeg is required for streaming; install FFmpeg and add it to PATH: ", "串流需要 ffmpeg；请安装 FFmpeg 并加入 PATH："},
	{"ffplay is required for streaming; install FFmpeg and add it to PATH: ", "串流需要 ffplay；请安装 FFmpeg 并加入 PATH："},
	{"Unable to send text: ", "无法发送文本："}, {"Stream cleanup failed; console was not powered off: ", "串流清理失败，未关闭主机："}, {"Unable to connect to cloud games: ", "无法连接云游戏："}, {"Unable to load cloud games: ", "无法加载云游戏："}, {"Estimated queue wait: ", "预计排队时间（分钟）："}, {"Connection timed out; remote session cleanup failed: ", "连接超时，且远程会话清理失败："}, {"Stream stopped; remote session cleanup failed: ", "串流已停止，但远程会话清理失败："}, {"Connection failed; remote session cleanup failed: ", "连接失败，且远程会话清理失败："}, {"Sign-in failed: ", "登录失败："}, {"Stream failed: ", "串流启动失败："}, {"Stream error: ", "串流错误："}, {"Wake request failed; continuing stream: ", "唤醒请求失败，继续尝试串流："}, {"Power-off request failed: ", "关机请求失败："}, {"Wake request failed: ", "唤醒请求失败："}, {"Unable to refresh consoles: ", "无法刷新主机："}, {"Unable to save settings: ", "无法保存设置："}, {"Signed out locally; unable to remove saved login: ", "已在本次运行中退出，但无法删除已保存的登录信息："},
}

func (a *app) tr(text string) string {
	if a.prefs.UILanguage != "简体中文" {
		return text
	}
	const persistenceSuffix = " Login could not be saved in the system credential store."
	if strings.HasSuffix(text, persistenceSuffix) {
		return a.tr(strings.TrimSuffix(text, persistenceSuffix)) + " 无法将登录信息保存到系统凭据库。"
	}
	if value, ok := chinese[text]; ok {
		return value
	}
	for _, item := range chinesePrefixes {
		if strings.HasPrefix(text, item.en) {
			suffix := strings.TrimPrefix(text, item.en)
			if item.en == "Estimated queue wait: " {
				suffix = strings.TrimSuffix(suffix, " minute(s).")
			}
			return item.zh + suffix
		}
	}
	if strings.HasPrefix(text, "Signed in. Found ") || strings.HasPrefix(text, "Found ") {
		text = strings.Replace(text, "Signed in. Found ", "已登录。找到 ", 1)
		text = strings.Replace(text, "Found ", "找到 ", 1)
		text = strings.Replace(text, " console(s).", " 台主机。", 1)
		return strings.Replace(text, " Login could not be saved in the system credential store.", " 无法将登录信息保存到系统凭据库。", 1)
	}
	if strings.Contains(text, " games") {
		text = strings.Replace(text, " games", " 个游戏", 1)
		return strings.Replace(text, " (some catalog names unavailable)", "（部分游戏名称不可用）", 1)
	}
	return text
}
