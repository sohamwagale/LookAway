package main

import (
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

var (
	lastAlertTime time.Time
	mu            sync.Mutex
)

// triggerAlert displays a native Windows Toast notification and speaks the warning
func triggerAlert() {
	mu.Lock()
	// Debounce alerts within 2 seconds to avoid echo/spam if multiple signals arrive rapidly
	if time.Since(lastAlertTime) < 2*time.Second {
		mu.Unlock()
		return
	}
	lastAlertTime = time.Now()
	mu.Unlock()

	go func() {
		// PowerShell script to trigger both Windows native Toast notification and Speech Synthesizer
		psScript := `
			# 1. Native Windows Toast Notification
			try {
				[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
				$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
				$textNodes = $template.GetElementsByTagName('text')
				$textNodes.Item(0).AppendChild($template.CreateTextNode('🚨 Phone Unlocked!')) | Out-Null
				$textNodes.Item(1).AppendChild($template.CreateTextNode('Put your phone down and focus!')) | Out-Null
				$toast = [Windows.UI.Notifications.ToastNotification]::new($template)
				[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('PhoneWarning').Show($toast)
			} catch {
				# Fallback if WinRT toast fails
				[System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms') | Out-Null
				$notify = New-Object System.Windows.Forms.NotifyIcon
				$notify.Icon = [System.Drawing.SystemIcons]::Warning
				$notify.Visible = $true
				$notify.ShowBalloonTip(3000, '🚨 Phone Unlocked!', 'Put your phone down and focus!', [System.Windows.Forms.ToolTipIcon]::Warning)
			}

			# 2. Audio Speech Alert
			try {
				$voice = New-Object -ComObject SAPI.SpVoice
				$voice.Speak('Warning! Phone unlocked! Put your phone down.')
			} catch {}
		`

		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
		err := cmd.Run()
		if err != nil {
			fmt.Println("Error showing notification:", err)
		}
	}()
}

func phoneUnlocked(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("[%s] 📱 Phone Unlocked detected!\n", time.Now().Format("15:04:05"))

	triggerAlert()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Alert triggered")
}

func main() {
	http.HandleFunc("/phone/unlocked", phoneUnlocked)

	fmt.Println("==================================================")
	fmt.Println("🚀 Laptop PhoneWarning Receiver Active")
	fmt.Println("🔊 Native Toast + Speech Voice Alert Enabled")
	fmt.Println("📡 Listening on port :8080...")
	fmt.Println("==================================================")

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}
