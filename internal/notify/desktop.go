package notify

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// DesktopChannel sends a transient OS-level notification. Linux uses
// notify-send; macOS uses osascript; Windows uses powershell. The
// channel is fire-and-forget by design — desktop notifications don't
// have a reliable reply mechanism, so we mark this as timeout
// (caller's on_timeout fallback applies).
//
// For an interactive desktop with reply, run a webhook channel pointed
// at a local listener that surfaces a UI of your choice.
type DesktopChannel struct{}

// NewDesktopChannel constructs the channel.
func NewDesktopChannel() *DesktopChannel { return &DesktopChannel{} }

// Name implements Channel.
func (DesktopChannel) Name() string { return "desktop" }

// Available reports whether the platform-specific binary is on PATH.
func (DesktopChannel) Available() bool {
	bin, _ := desktopBinary()
	if bin == "" {
		return false
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// Ask implements Channel.
func (DesktopChannel) Ask(ctx context.Context, req EscalateRequest) (Reply, error) {
	bin, args := desktopBinaryArgs(req)
	if bin == "" {
		return Reply{Timeout: true}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	if err := cmd.Run(); err != nil {
		return Reply{}, err
	}
	// Desktop notify is non-blocking → treat as timeout so on_timeout
	// fallback drives the actual decision.
	return Reply{Timeout: true, Channel: "desktop"}, nil
}

func desktopBinary() (string, []string) {
	switch runtime.GOOS {
	case "linux":
		return "notify-send", nil
	case "darwin":
		return "osascript", nil
	case "windows":
		return "powershell", nil
	}
	return "", nil
}

func desktopBinaryArgs(req EscalateRequest) (string, []string) {
	bin, _ := desktopBinary()
	switch bin {
	case "notify-send":
		return bin, []string{"--app-name=godx-arbiter", "godx-arbiter", req.Question}
	case "osascript":
		script := fmt.Sprintf(`display notification %q with title "godx-arbiter"`, req.Question)
		return bin, []string{"-e", script}
	case "powershell":
		// Use a toast — minimal embed; no return value.
		ps := fmt.Sprintf(`[void][Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime];$xml=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02);$xml.GetElementsByTagName("text")[0].AppendChild($xml.CreateTextNode("godx-arbiter"))|Out-Null;$xml.GetElementsByTagName("text")[1].AppendChild($xml.CreateTextNode(%q))|Out-Null;[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier("godx-arbiter").Show([Windows.UI.Notifications.ToastNotification]::new($xml))`, req.Question)
		return bin, []string{"-Command", ps}
	}
	return "", nil
}
