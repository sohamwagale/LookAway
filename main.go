package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"
)

// hiddenCmd creates an exec.Cmd that runs without a visible console window.
// On Windows, CREATE_NO_WINDOW (0x08000000) prevents a terminal from popping up.
func hiddenCmd(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	return cmd
}

// Session represents a single phone unlock session
type Session struct {
	ID        int       `json:"id"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
	Duration  string    `json:"duration"`
	Seconds   int       `json:"seconds"`
}

// Stats holds all analytical metrics
type Stats struct {
	IsUnlocked            bool      `json:"isUnlocked"`
	CurrentStreakSeconds  int       `json:"currentStreakSeconds"`
	TotalUnlocksToday     int       `json:"totalUnlocksToday"`
	TotalScreenTimeSec    int       `json:"totalScreenTimeSeconds"`
	FormattedScreenTime   string    `json:"formattedScreenTime"`
	AvgSessionSeconds     int       `json:"avgSessionSeconds"`
	FormattedAvgSession   string    `json:"formattedAvgSession"`
	LastUnlockTime        time.Time `json:"lastUnlockTime"`
	LastLockTime          time.Time `json:"lastLockTime"`
	RecentUnlockFrequency int       `json:"recentUnlockFrequency"`
	Sessions              []Session `json:"sessions"`
}

// StateManager manages thread-safe app state and SSE broadcast
type StateManager struct {
	mu           sync.Mutex
	isUnlocked   bool
	unlockTime   time.Time
	lockTime     time.Time
	unlockTimes  []time.Time
	sessions     []Session
	sessionCount int
	clients      map[chan string]bool
}

var state = &StateManager{
	lockTime: time.Now(),
	clients:  make(map[chan string]bool),
}

// AlertManager handles repeating audio, toasts, and visual screen flashes
type AlertManager struct {
	mu       sync.Mutex
	stopChan chan struct{}
	active   bool
}

var alertManager = &AlertManager{}

// Dynamic voice line pools based on escalation tiers
var tier1Lines = []string{
	"Warning! Put your phone down.",
	"Focus on your screen, not your phone.",
	"Phone unlocked. Get back to work.",
	"Stay focused on your task.",
	"Put the phone away.",
}

var tier2Lines = []string{
	"You unlocked your phone again! Put it away.",
	"Stop checking your phone every two minutes!",
	"Your future self is watching. Drop the phone.",
	"Distraction detected! Focus!",
	"Why are you on your phone again?",
}

var tier3Lines = []string{
	"Seriously?! Step away from the phone right now!",
	"Emergency! Extreme distraction detected! Drop the phone!",
	"Code will not write itself! Put the phone down!",
	"Zero discipline! Put it away!",
}

var lockPraiseLines = []string{
	"Good job.",
	"Good boy.",
	"Phone locked. Back to focus.",
	"Nice discipline.",
	"Focused mode restored.",
}

func (sm *StateManager) RecordUnlock() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	sm.isUnlocked = true
	sm.unlockTime = now
	sm.unlockTimes = append(sm.unlockTimes, now)

	// Clean up unlock times older than 10 minutes for frequency calculation
	recentCount := 0
	for _, t := range sm.unlockTimes {
		if now.Sub(t) <= 10*time.Minute {
			recentCount++
		}
	}

	sm.broadcastUpdate()
	return recentCount
}

func (sm *StateManager) RecordLock() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.isUnlocked {
		return
	}

	now := time.Now()
	sm.isUnlocked = false
	sm.lockTime = now
	durationSec := int(now.Sub(sm.unlockTime).Seconds())
	if durationSec < 1 {
		durationSec = 1
	}

	sm.sessionCount++
	session := Session{
		ID:        sm.sessionCount,
		StartTime: sm.unlockTime,
		EndTime:   now,
		Seconds:   durationSec,
		Duration:  formatDuration(durationSec),
	}

	sm.sessions = append([]Session{session}, sm.sessions...) // newest first
	sm.broadcastUpdate()
}

func (sm *StateManager) GetStats() Stats {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	streakSec := 0
	if !sm.isUnlocked {
		streakSec = int(now.Sub(sm.lockTime).Seconds())
	}

	totalScreenTime := 0
	for _, s := range sm.sessions {
		totalScreenTime += s.Seconds
	}
	if sm.isUnlocked {
		totalScreenTime += int(now.Sub(sm.unlockTime).Seconds())
	}

	avgSec := 0
	if len(sm.sessions) > 0 {
		avgSec = totalScreenTime / (len(sm.sessions) + boolToInt(sm.isUnlocked))
	}

	recentFreq := 0
	for _, t := range sm.unlockTimes {
		if now.Sub(t) <= 10*time.Minute {
			recentFreq++
		}
	}

	return Stats{
		IsUnlocked:            sm.isUnlocked,
		CurrentStreakSeconds:  streakSec,
		TotalUnlocksToday:     len(sm.unlockTimes),
		TotalScreenTimeSec:    totalScreenTime,
		FormattedScreenTime:   formatDuration(totalScreenTime),
		AvgSessionSeconds:     avgSec,
		FormattedAvgSession:   formatDuration(avgSec),
		LastUnlockTime:        sm.unlockTime,
		LastLockTime:          sm.lockTime,
		RecentUnlockFrequency: recentFreq,
		Sessions:              sm.sessions,
	}
}

func (sm *StateManager) broadcastUpdate() {
	msg := "update"
	for ch := range sm.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func formatDuration(totalSeconds int) string {
	if totalSeconds < 60 {
		return fmt.Sprintf("%ds", totalSeconds)
	}
	m := totalSeconds / 60
	s := totalSeconds % 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh %dm", h, m)
}

// Start initiates the repeating audio alert, toast, and visual flash
func (m *AlertManager) Start(recentUnlocks int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active {
		return
	}

	m.active = true
	m.stopChan = make(chan struct{})

	// 1. Trigger Native Windows Toast
	go showToastNotification("🚨 Phone Unlocked!", "Put your phone down and stay focused!")

	// 2. Trigger Visual Red Screen Flash
	go triggerVisualScreenFlash()

	// 3. Start Repeating Audio Alert Loop (every 5 seconds)
	go func(stop chan struct{}, frequency int) {
		fmt.Printf("[%s] 🔊 Escalating alert started (repeating every 5s)...\n", time.Now().Format("15:04:05"))

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		// Helper: play voice only if not stopped
		speak := func() {
			select {
			case <-stop:
				return
			default:
				playEscalatedVoice(frequency)
			}
		}

		// Play immediately on unlock
		speak()

		for {
			select {
			case <-stop:
				fmt.Printf("[%s] ⏹️ Alert stopped (phone locked).\n", time.Now().Format("15:04:05"))
				return
			case <-ticker.C:
				go triggerVisualScreenFlash()
				speak()
			}
		}
	}(m.stopChan, recentUnlocks)
}

// Stop cancels the repeating alert and plays praise audio
func (m *AlertManager) Stop() {
	m.mu.Lock()
	if m.active {
		close(m.stopChan)
		m.active = false
	}
	m.mu.Unlock()

	go playLockPraiseVoice()
}

func playEscalatedVoice(recentUnlocks int) {
	var phrase string
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	if recentUnlocks >= 5 {
		phrase = tier3Lines[r.Intn(len(tier3Lines))]
	} else if recentUnlocks >= 3 {
		phrase = tier2Lines[r.Intn(len(tier2Lines))]
	} else {
		phrase = tier1Lines[r.Intn(len(tier1Lines))]
	}

	psScript := fmt.Sprintf(`
		Add-Type -AssemblyName System.Speech
		$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer
		$synth.Rate = 1
		$synth.Volume = 100
		$synth.Speak('%s')
	`, phrase)

	cmd := hiddenCmd("powershell", "-STA", "-NoProfile", "-NonInteractive", "-Command", psScript)
	_ = cmd.Run()
}

func playLockPraiseVoice() {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	phrase := lockPraiseLines[r.Intn(len(lockPraiseLines))]

	psScript := fmt.Sprintf(`
		Add-Type -AssemblyName System.Speech
		$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer
		$synth.Rate = 0
		$synth.Volume = 100
		$synth.Speak('%s')
	`, phrase)

	cmd := hiddenCmd("powershell", "-STA", "-NoProfile", "-NonInteractive", "-Command", psScript)
	_ = cmd.Run()
}

// triggerVisualScreenFlash displays a semi-transparent red screen flash overlay
func triggerVisualScreenFlash() {
	psScript := `
		Add-Type -AssemblyName System.Windows.Forms
		Add-Type -AssemblyName System.Drawing
		$form = New-Object System.Windows.Forms.Form
		$form.FormBorderStyle = [System.Windows.Forms.FormBorderStyle]::None
		$form.WindowState = [System.Windows.Forms.FormWindowState]::Maximized
		$form.TopMost = $true
		$form.BackColor = [System.Drawing.Color]::Red
		$form.Opacity = 0.28
		$form.ShowInTaskbar = $false
		$form.StartPosition = [System.Windows.Forms.FormStartPosition]::Manual
		$form.Show()
		Start-Sleep -Milliseconds 450
		$form.Close()
	`
	cmd := hiddenCmd("powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	_ = cmd.Run()
}

func showToastNotification(title, message string) {
	// Use a WinForms NotifyIcon with a proper message pump so the balloon
	// tip actually appears. Application.Run keeps the message loop alive
	// long enough for the tip to show, then exits after the timeout.
	psScript := fmt.Sprintf(`
		Add-Type -AssemblyName System.Windows.Forms
		Add-Type -AssemblyName System.Drawing
		$notify = New-Object System.Windows.Forms.NotifyIcon
		$notify.Icon = [System.Drawing.SystemIcons]::Information
		$notify.Visible = $true
		$notify.BalloonTipTitle = '%s'
		$notify.BalloonTipText  = '%s'
		$notify.BalloonTipIcon  = [System.Windows.Forms.ToolTipIcon]::Warning
		$notify.ShowBalloonTip(4000)
		Start-Sleep -Milliseconds 4500
		$notify.Visible = $false
		$notify.Dispose()
	`, title, message)

	cmd := hiddenCmd("powershell", "-STA", "-NoProfile", "-NonInteractive", "-Command", psScript)
	_ = cmd.Run()
}

// HTTP Handlers

func phoneUnlocked(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("[%s] 📱 Phone Unlocked detected!\n", time.Now().Format("15:04:05"))
	recentCount := state.RecordUnlock()
	alertManager.Start(recentCount)

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Unlocked: Alert started")
}

func phoneLocked(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("[%s] 🔒 Phone Locked detected!\n", time.Now().Format("15:04:05"))
	state.RecordLock()
	alertManager.Stop()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Locked: Alert stopped")
}

func apiStatsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(state.GetStats())
}

func sseEventsHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientChan := make(chan string)
	state.mu.Lock()
	state.clients[clientChan] = true
	state.mu.Unlock()

	defer func() {
		state.mu.Lock()
		delete(state.clients, clientChan)
		state.mu.Unlock()
	}()

	// Send initial state
	fmt.Fprintf(w, "data: init\n\n")
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case msg := <-clientChan:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

func openBrowser(url string) {
	cmd := hiddenCmd("rundll32", "url.dll,FileProtocolHandler", url)
	_ = cmd.Start()
}

func main() {
	http.HandleFunc("/", dashboardHandler)
	http.HandleFunc("/api/stats", apiStatsHandler)
	http.HandleFunc("/api/events", sseEventsHandler)
	http.HandleFunc("/phone/unlocked", phoneUnlocked)
	http.HandleFunc("/phone/locked", phoneLocked)

	fmt.Println("================================================================")
	fmt.Println("🚀 Laptop LookAway Hub Active")
	fmt.Println("🔊 Escalating Voice Warnings + Lock Praise")
	fmt.Println("🚨 Native Windows Toast + Red Visual Screen Flash")
	fmt.Println("📊 Web Analytics Dashboard: http://localhost:8080")
	fmt.Println("📡 Listening on port :8080...")
	fmt.Println("📌 System Tray Icon active in taskbar notification area")
	fmt.Println("================================================================")

	// Start web server in background goroutine
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil && err != http.ErrServerClosed {
			fmt.Printf("HTTP Server error: %v\n", err)
		}
	}()

	// Start system tray loop (must run on main thread)
	systray.Run(onSystrayReady, onSystrayExit)
}

func onSystrayReady() {
	systray.SetIcon(getAppIconICO())
	systray.SetTitle("LookAway")
	systray.SetTooltip("LookAway - Phone Distraction Blocker")

	mStatus := systray.AddMenuItem("🟢 LookAway Active (:8080)", "LookAway server is running")
	mStatus.Disable()

	systray.AddSeparator()

	mOpen := systray.AddMenuItem("🌐 Open Dashboard", "Open analytics dashboard in browser")

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("❌ Exit LookAway", "Stop server and quit application")

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				openBrowser("http://localhost:8080")
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func onSystrayExit() {
	alertManager.Stop()
	os.Exit(0)
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>PhoneWarning • Focus Analytics</title>
    <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;700;900&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg: #0d0f17;
            --card-bg: rgba(22, 27, 46, 0.7);
            --card-border: rgba(255, 255, 255, 0.08);
            --accent-green: #00f59b;
            --accent-red: #ff3860;
            --accent-purple: #9d4edd;
            --accent-blue: #00b4d8;
            --text-primary: #ffffff;
            --text-secondary: #94a3b8;
        }

        * {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
            font-family: 'Outfit', sans-serif;
        }

        body {
            background: var(--bg);
            background-image: 
                radial-gradient(circle at 15% 15%, rgba(157, 78, 221, 0.15) 0%, transparent 40%),
                radial-gradient(circle at 85% 85%, rgba(0, 245, 155, 0.1) 0%, transparent 40%);
            color: var(--text-primary);
            min-height: 100vh;
            padding: 32px 20px;
        }

        .container {
            max-width: 1080px;
            margin: 0 auto;
        }

        header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 32px;
            padding-bottom: 20px;
            border-bottom: 1px solid var(--card-border);
        }

        .logo-group h1 {
            font-size: 28px;
            font-weight: 800;
            letter-spacing: -0.5px;
            background: linear-gradient(135deg, #fff, #94a3b8);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
        }

        .logo-group p {
            color: var(--text-secondary);
            font-size: 14px;
            margin-top: 4px;
        }

        /* Live Status Banner */
        .status-banner {
            display: flex;
            align-items: center;
            justify-content: space-between;
            padding: 24px 32px;
            border-radius: 20px;
            margin-bottom: 28px;
            transition: all 0.4s ease;
            backdrop-filter: blur(12px);
            border: 1px solid var(--card-border);
        }

        .status-banner.locked {
            background: rgba(0, 245, 155, 0.08);
            border-color: rgba(0, 245, 155, 0.3);
            box-shadow: 0 0 30px rgba(0, 245, 155, 0.1);
        }

        .status-banner.unlocked {
            background: rgba(255, 56, 96, 0.12);
            border-color: rgba(255, 56, 96, 0.4);
            box-shadow: 0 0 40px rgba(255, 56, 96, 0.2);
            animation: pulse-red 2s infinite;
        }

        @keyframes pulse-red {
            0%, 100% { transform: scale(1); }
            50% { transform: scale(1.008); }
        }

        .status-left {
            display: flex;
            align-items: center;
            gap: 18px;
        }

        .status-dot {
            width: 18px;
            height: 18px;
            border-radius: 50%;
            background: var(--accent-green);
            box-shadow: 0 0 16px var(--accent-green);
            transition: all 0.3s ease;
        }

        .status-banner.unlocked .status-dot {
            background: var(--accent-red);
            box-shadow: 0 0 20px var(--accent-red);
        }

        .status-title {
            font-size: 22px;
            font-weight: 700;
        }

        .status-subtitle {
            font-size: 14px;
            color: var(--text-secondary);
            margin-top: 2px;
        }

        .streak-pill {
            background: rgba(255, 255, 255, 0.06);
            padding: 10px 20px;
            border-radius: 40px;
            font-size: 15px;
            font-weight: 600;
            border: 1px solid var(--card-border);
        }

        /* Stats Grid */
        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
            gap: 20px;
            margin-bottom: 32px;
        }

        .stat-card {
            background: var(--card-bg);
            border: 1px solid var(--card-border);
            border-radius: 18px;
            padding: 24px;
            backdrop-filter: blur(12px);
            transition: transform 0.2s ease, border-color 0.2s ease;
        }

        .stat-card:hover {
            transform: translateY(-3px);
            border-color: rgba(255, 255, 255, 0.2);
        }

        .stat-label {
            font-size: 13px;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.8px;
            color: var(--text-secondary);
            margin-bottom: 12px;
        }

        .stat-val {
            font-size: 32px;
            font-weight: 900;
            letter-spacing: -0.5px;
        }

        /* History Table */
        .history-card {
            background: var(--card-bg);
            border: 1px solid var(--card-border);
            border-radius: 20px;
            padding: 28px;
            backdrop-filter: blur(12px);
        }

        .history-header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 20px;
        }

        .history-header h2 {
            font-size: 20px;
            font-weight: 700;
        }

        table {
            width: 100%;
            border-collapse: collapse;
            text-align: left;
        }

        th {
            font-size: 12px;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.6px;
            color: var(--text-secondary);
            padding: 12px 16px;
            border-bottom: 1px solid var(--card-border);
        }

        td {
            padding: 16px;
            font-size: 14px;
            border-bottom: 1px solid rgba(255, 255, 255, 0.04);
        }

        tr:hover td {
            background: rgba(255, 255, 255, 0.02);
        }

        .badge-duration {
            background: rgba(255, 56, 96, 0.15);
            color: #ff6b8b;
            padding: 4px 10px;
            border-radius: 20px;
            font-size: 12px;
            font-weight: 600;
            display: inline-block;
        }

        .empty-state {
            text-align: center;
            padding: 40px;
            color: var(--text-secondary);
            font-size: 15px;
        }
    </style>
</head>
<body>
    <div class="container">
        <header>
            <div class="logo-group">
                <h1>🛡️ PhoneWarning Hub</h1>
                <p>Real-time distraction defense & focus metrics</p>
            </div>
            <div id="liveClock" style="color: var(--text-secondary); font-size: 14px; font-weight: 600;"></div>
        </header>

        <!-- Live Status Banner -->
        <div id="statusBanner" class="status-banner locked">
            <div class="status-left">
                <div class="status-dot"></div>
                <div>
                    <div id="statusTitle" class="status-title">🟢 Phone Locked</div>
                    <div id="statusSubtitle" class="status-subtitle">Focus mode is active. Keep working!</div>
                </div>
            </div>
            <div id="streakPill" class="streak-pill">⏱️ Focus Streak: 0s</div>
        </div>

        <!-- Stats Grid -->
        <div class="stats-grid">
            <div class="stat-card">
                <div class="stat-label">Total Unlocks Today</div>
                <div id="totalUnlocks" class="stat-val" style="color: var(--accent-purple);">0</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Total Phone Screen Time</div>
                <div id="totalScreenTime" class="stat-val" style="color: var(--accent-blue);">0s</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">Avg Session Duration</div>
                <div id="avgDuration" class="stat-val" style="color: var(--accent-green);">0s</div>
            </div>
            <div class="stat-card">
                <div class="stat-label">10m Unlock Frequency</div>
                <div id="recentFreq" class="stat-val" style="color: var(--accent-red);">0</div>
            </div>
        </div>

        <!-- History Table -->
        <div class="history-card">
            <div class="history-header">
                <h2>📋 Unlock Sessions Today</h2>
            </div>
            <div id="tableContainer">
                <table>
                    <thead>
                        <tr>
                            <th>#</th>
                            <th>Time</th>
                            <th>Session Duration</th>
                            <th>Status</th>
                        </tr>
                    </thead>
                    <tbody id="sessionBody">
                        <tr>
                            <td colspan="4" class="empty-state">No unlock sessions recorded yet today. Keep it up!</td>
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>
    </div>

    <script>
        function updateClock() {
            var now = new Date();
            document.getElementById('liveClock').innerText = now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
        }
        setInterval(updateClock, 1000);
        updateClock();

        var currentStreak = 0;
        var isPhoneUnlocked = false;

        function formatDuration(sec) {
            if (sec < 60) return sec + 's';
            var m = Math.floor(sec / 60);
            var s = sec % 60;
            if (m < 60) return m + 'm ' + s + 's';
            var h = Math.floor(m / 60);
            return h + 'h ' + (m % 60) + 'm';
        }

        async function fetchStats() {
            try {
                var res = await fetch('/api/stats');
                var data = await res.json();
                
                isPhoneUnlocked = data.isUnlocked;
                currentStreak = data.currentStreakSeconds;

                // Update Status Banner
                var banner = document.getElementById('statusBanner');
                var title = document.getElementById('statusTitle');
                var subtitle = document.getElementById('statusSubtitle');
                var streakPill = document.getElementById('streakPill');

                if (data.isUnlocked) {
                    banner.className = 'status-banner unlocked';
                    title.innerText = '🚨 PHONE UNLOCKED!';
                    subtitle.innerText = 'Warning audio repeating! Lock your phone to resume focus.';
                    streakPill.innerText = '⚠️ Warning Active';
                    streakPill.style.color = '#ff3860';
                } else {
                    banner.className = 'status-banner locked';
                    title.innerText = '🟢 Phone Locked';
                    subtitle.innerText = 'Focus mode is active. Keep working!';
                    streakPill.innerText = '⏱️ Focus Streak: ' + formatDuration(currentStreak);
                    streakPill.style.color = '#00f59b';
                }

                // Update Stat Cards
                document.getElementById('totalUnlocks').innerText = data.totalUnlocksToday;
                document.getElementById('totalScreenTime').innerText = data.formattedScreenTime;
                document.getElementById('avgDuration').innerText = data.formattedAvgSession;
                document.getElementById('recentFreq').innerText = data.recentUnlockFrequency;

                // Update Table
                var tbody = document.getElementById('sessionBody');
                if (!data.sessions || data.sessions.length === 0) {
                    tbody.innerHTML = '<tr><td colspan="4" class="empty-state">No unlock sessions recorded yet today. Keep it up!</td></tr>';
                } else {
                    var rowsHtml = '';
                    for (var i = 0; i < data.sessions.length; i++) {
                        var s = data.sessions[i];
                        var timeStr = new Date(s.startTime).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
                        rowsHtml += '<tr>' +
                            '<td style="color: var(--text-secondary); font-weight: 600;">#' + s.id + '</td>' +
                            '<td style="font-weight: 600;">' + timeStr + '</td>' +
                            '<td><span class="badge-duration">' + s.duration + '</span></td>' +
                            '<td style="color: #94a3b8;">Completed</td>' +
                        '</tr>';
                    }
                    tbody.innerHTML = rowsHtml;
                }
            } catch (err) {
                console.error("Failed to fetch stats:", err);
            }
        }

        // Live local streak incrementer
        setInterval(function() {
            if (!isPhoneUnlocked) {
                currentStreak++;
                document.getElementById('streakPill').innerText = '⏱️ Focus Streak: ' + formatDuration(currentStreak);
            }
        }, 1000);

        // Server-Sent Events (SSE) for instant live updates
        var evtSource = new EventSource('/api/events');
        evtSource.onmessage = function() {
            fetchStats();
        };

        fetchStats();
    </script>
</body>
</html>`
