package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/defomok-max/Ties/internal/agent"
	"github.com/defomok-max/Ties/internal/pricing"
	"github.com/defomok-max/Ties/internal/provider"
	"github.com/defomok-max/Ties/internal/screen"
	"github.com/defomok-max/Ties/internal/session"
	"github.com/defomok-max/Ties/internal/tui"
	"github.com/defomok-max/Ties/internal/ui"
)

// errFallbackChat signals that the interactive screen could not start and the
// caller should fall back to the line-oriented chat loop.
var errFallbackChat = errors.New("chat: interactive screen unavailable")

// liveSink is the minimal surface the agent callbacks use to push streaming
// output into an interactive UI. The chat screen implements it.
type liveSink interface {
	appendAssistant(delta string)
	endAssistant()
	liveEmpty() bool
	addTool(name, detail string)
	addError(text string)
	setWorking(on bool)
	setUsage(in, out int, cost float64, hasCost bool)
}

// chatKind classifies a transcript entry.
type chatKind int

const (
	ckUser chatKind = iota
	ckAssistant
	ckTool
	ckError
	ckNote
)

type chatEntry struct {
	kind   chatKind
	text   string
	detail string
}

// slashItem is a documented slash command shown in the autocomplete popup.
type slashItem struct {
	name string
	desc string
}

var chatCommands = []slashItem{
	{"/help", "show commands"},
	{"/model", "active model"},
	{"/tools", "list available tools"},
	{"/skills", "list loaded skills"},
	{"/context", "loaded context files"},
	{"/usage", "token usage & cost"},
	{"/clear", "clear the transcript"},
	{"/exit", "quit ties"},
}

// chatUI is a raw-mode, mouse-aware, OpenCode-style full-screen chat surface.
// All mutable state is guarded by mu; renderLocked paints a fresh frame.
type chatUI struct {
	sc    *screen.Screen
	theme ui.Theme
	color bool

	mu      sync.Mutex
	entries []chatEntry
	live    string
	hasLive bool

	input  []rune
	cur    int
	inOff  int // horizontal scroll offset within the input field
	scroll int // body lines scrolled up from the bottom

	working bool
	spin    int

	model   string
	session string
	mode    string // "plan", "tdd", "loop" … shown in the status bar
	tokIn   int
	tokOut  int
	cost    float64
	hasCost bool

	// slash popup
	popup    bool
	popupSel int
	popMatch []slashItem
	popRow0  int // 1-based terminal row of the first popup item (for clicks)

	// modal approval prompt
	modal     bool
	modalText string
	modalResp chan bool

	busy   bool
	cancel context.CancelFunc

	width  int
	height int
	quit   bool
}

func newChatUI(sc *screen.Screen, theme ui.Theme, color bool, model, sessionID string) *chatUI {
	w, h := sc.Size()
	return &chatUI{sc: sc, theme: theme, color: color, model: model, session: sessionID, width: w, height: h}
}

func (c *chatUI) sgr(code, s string) string { return ui.SGR(code, s, c.color) }

// --- liveSink implementation (called from the agent goroutine) --------------

func (c *chatUI) appendAssistant(delta string) {
	c.mu.Lock()
	c.working = false
	c.live += delta
	c.hasLive = true
	c.scroll = 0
	c.renderLocked()
	c.mu.Unlock()
}

func (c *chatUI) endAssistant() {
	c.mu.Lock()
	c.endAssistantLocked()
	c.renderLocked()
	c.mu.Unlock()
}

func (c *chatUI) endAssistantLocked() {
	if c.hasLive {
		if strings.TrimSpace(c.live) != "" {
			c.entries = append(c.entries, chatEntry{kind: ckAssistant, text: c.live})
		}
		c.live = ""
		c.hasLive = false
		c.scroll = 0
	}
}

func (c *chatUI) liveEmpty() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.live) == ""
}

func (c *chatUI) addTool(name, detail string) {
	c.mu.Lock()
	c.endAssistantLocked()
	c.entries = append(c.entries, chatEntry{kind: ckTool, text: name, detail: detail})
	c.working = true
	c.scroll = 0
	c.renderLocked()
	c.mu.Unlock()
}

func (c *chatUI) addError(text string) {
	c.mu.Lock()
	c.entries = append(c.entries, chatEntry{kind: ckError, text: text})
	c.scroll = 0
	c.renderLocked()
	c.mu.Unlock()
}

func (c *chatUI) setWorking(on bool) {
	c.mu.Lock()
	c.working = on
	c.renderLocked()
	c.mu.Unlock()
}

func (c *chatUI) setUsage(in, out int, cost float64, hasCost bool) {
	c.mu.Lock()
	c.tokIn, c.tokOut, c.cost, c.hasCost = in, out, cost, hasCost
	c.renderLocked()
	c.mu.Unlock()
}

// confirm implements an in-screen yes/no approval modal. It is called from the
// agent goroutine and blocks until the user answers via the event loop.
func (c *chatUI) confirm(name, target string) bool {
	ch := make(chan bool, 1)
	c.mu.Lock()
	c.modal = true
	c.modalText = "Allow " + name + " on " + target + "?"
	c.modalResp = ch
	c.renderLocked()
	c.mu.Unlock()
	return <-ch
}

// --- locked helpers ---------------------------------------------------------

func (c *chatUI) addEntryLocked(k chatKind, text, detail string) {
	c.entries = append(c.entries, chatEntry{kind: k, text: text, detail: detail})
	c.scroll = 0
}

func (c *chatUI) addNote(text string) {
	c.mu.Lock()
	c.addEntryLocked(ckNote, text, "")
	c.renderLocked()
	c.mu.Unlock()
}

// --- event loop -------------------------------------------------------------

// run drives the screen: it starts the spinner/resize ticker and processes
// input events until the user quits.
func (c *chatUI) run(ctx context.Context, a *app, ag *agent.Agent, model string, usage *usageMeter) error {
	stop := make(chan struct{})
	go c.ticker(stop)
	defer close(stop)

	c.mu.Lock()
	c.renderLocked()
	c.mu.Unlock()

	for {
		evs, err := c.sc.Read()
		if err != nil {
			return nil
		}
		for _, ev := range evs {
			c.handleEvent(ctx, ev, a, ag, model, usage)
		}
		c.mu.Lock()
		q := c.quit
		c.mu.Unlock()
		if q {
			return nil
		}
	}
}

// ticker repaints periodically so the spinner animates and terminal resizes are
// picked up even while Read blocks between keystrokes.
func (c *chatUI) ticker(stop <-chan struct{}) {
	t := time.NewTicker(110 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c.mu.Lock()
			if c.sc.Refresh() {
				c.width, c.height = c.sc.Size()
			}
			if c.working {
				c.spin++
			}
			c.renderLocked()
			c.mu.Unlock()
		}
	}
}

func (c *chatUI) handleEvent(ctx context.Context, ev screen.Event, a *app, ag *agent.Agent, model string, usage *usageMeter) {
	if ev.Kind == screen.EventMouse {
		c.handleMouse(ev.Mouse)
		return
	}
	c.mu.Lock()
	if c.modal {
		c.handleModalKey(ev)
		c.renderLocked()
		c.mu.Unlock()
		return
	}
	submit, line := c.handleKeyLocked(ev)
	c.renderLocked()
	c.mu.Unlock()
	if submit {
		c.dispatch(ctx, a, ag, model, usage, line)
	}
}

func (c *chatUI) handleModalKey(ev screen.Event) {
	answer := func(v bool) {
		if c.modalResp != nil {
			c.modalResp <- v
			c.modalResp = nil
		}
		c.modal = false
	}
	switch ev.Key {
	case screen.KeyEnter:
		answer(true)
	case screen.KeyEsc:
		answer(false)
	case screen.KeyRune:
		switch ev.Rune {
		case 'y', 'Y':
			answer(true)
		case 'n', 'N':
			answer(false)
		}
	}
}

// handleKeyLocked mutates input/scroll state and returns (submit,line) when the
// user pressed Enter on a non-empty line.
func (c *chatUI) handleKeyLocked(ev screen.Event) (bool, string) {
	switch ev.Key {
	case screen.KeyCtrlC:
		if c.busy && c.cancel != nil {
			c.cancel()
			c.addEntryLocked(ckNote, "⏹ interrupted", "")
			return false, ""
		}
		c.quit = true
		return false, ""
	case screen.KeyEsc:
		if c.popup {
			c.popup = false
		} else {
			c.input = c.input[:0]
			c.cur = 0
		}
		c.refreshPopup()
		return false, ""
	case screen.KeyEnter:
		line := strings.TrimSpace(string(c.input))
		c.input = c.input[:0]
		c.cur = 0
		c.popup = false
		c.scroll = 0
		if line == "" {
			return false, ""
		}
		return true, line
	case screen.KeyTab:
		if c.popup && len(c.popMatch) > 0 {
			c.complete(c.popMatch[c.popupSel])
		}
		return false, ""
	case screen.KeyUp:
		if c.popup && len(c.popMatch) > 0 {
			c.popupSel = (c.popupSel - 1 + len(c.popMatch)) % len(c.popMatch)
		} else {
			c.scroll++
		}
		return false, ""
	case screen.KeyDown:
		if c.popup && len(c.popMatch) > 0 {
			c.popupSel = (c.popupSel + 1) % len(c.popMatch)
		} else if c.scroll > 0 {
			c.scroll--
		}
		return false, ""
	case screen.KeyPgUp:
		c.scroll += c.bodyHeight() - 1
		return false, ""
	case screen.KeyPgDn:
		c.scroll -= c.bodyHeight() - 1
		if c.scroll < 0 {
			c.scroll = 0
		}
		return false, ""
	case screen.KeyLeft:
		if c.cur > 0 {
			c.cur--
		}
		return false, ""
	case screen.KeyRight:
		if c.cur < len(c.input) {
			c.cur++
		}
		return false, ""
	case screen.KeyHome:
		c.cur = 0
		return false, ""
	case screen.KeyEnd:
		c.cur = len(c.input)
		return false, ""
	case screen.KeyBackspace:
		if c.cur > 0 {
			c.input = append(c.input[:c.cur-1], c.input[c.cur:]...)
			c.cur--
		}
		c.refreshPopup()
		return false, ""
	case screen.KeyDelete:
		if c.cur < len(c.input) {
			c.input = append(c.input[:c.cur], c.input[c.cur+1:]...)
		}
		c.refreshPopup()
		return false, ""
	case screen.KeyRune:
		c.input = append(c.input[:c.cur], append([]rune{ev.Rune}, c.input[c.cur:]...)...)
		c.cur++
		c.refreshPopup()
		return false, ""
	}
	return false, ""
}

func (c *chatUI) handleMouse(m screen.Mouse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case m.Wheel < 0:
		c.scroll += 3
	case m.Wheel > 0:
		c.scroll -= 3
		if c.scroll < 0 {
			c.scroll = 0
		}
	case m.Press && m.Button == 0 && c.popup && c.popRow0 > 0:
		idx := m.Y - c.popRow0
		if idx >= 0 && idx < len(c.popMatch) {
			c.popupSel = idx
			c.complete(c.popMatch[idx])
		}
	}
	c.renderLocked()
}

// complete fills the input with the chosen command (plus a trailing space for
// commands that take arguments).
func (c *chatUI) complete(it slashItem) {
	c.input = []rune(it.name + " ")
	c.cur = len(c.input)
	c.popup = false
}

// refreshPopup recomputes whether the slash autocomplete popup is shown.
func (c *chatUI) refreshPopup() {
	s := string(c.input)
	if !strings.HasPrefix(s, "/") || strings.ContainsAny(s, " \t") {
		c.popup = false
		c.popMatch = nil
		return
	}
	var m []slashItem
	for _, cmd := range chatCommands {
		if strings.HasPrefix(cmd.name, s) {
			m = append(m, cmd)
		}
	}
	c.popMatch = m
	c.popup = len(m) > 0
	if c.popupSel >= len(m) {
		c.popupSel = 0
	}
}

// dispatch runs a submitted line: a slash command, or a new agent turn.
func (c *chatUI) dispatch(ctx context.Context, a *app, ag *agent.Agent, model string, usage *usageMeter, line string) {
	if strings.HasPrefix(line, "/") {
		if a.chatSlash(c, line, model, usage) {
			c.mu.Lock()
			c.quit = true
			c.mu.Unlock()
		}
		return
	}
	c.mu.Lock()
	if c.busy {
		c.addEntryLocked(ckNote, "still working… press ctrl+c to stop", "")
		c.renderLocked()
		c.mu.Unlock()
		return
	}
	c.busy = true
	c.working = true
	c.addEntryLocked(ckUser, line, "")
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.renderLocked()
	c.mu.Unlock()

	go func() {
		err := ag.Run(runCtx, expandMentions(a.root, line))
		cancel()
		c.mu.Lock()
		c.endAssistantLocked()
		c.working = false
		c.busy = false
		c.cancel = nil
		if err != nil && !errors.Is(err, context.Canceled) {
			c.addEntryLocked(ckError, err.Error(), "")
		}
		c.renderLocked()
		c.mu.Unlock()
	}()
}

// --- rendering --------------------------------------------------------------

func (c *chatUI) bodyHeight() int {
	h := c.height - 2 /*header*/ - 3 /*input box*/ - 1 /*hint*/
	if h < 1 {
		return 1
	}
	return h
}

func (c *chatUI) renderLocked() {
	w, h := c.width, c.height
	if w < 24 || h < 9 {
		c.sc.BeginFrame()
		c.sc.WriteLine(" ties — window too small")
		c.sc.EndFrame()
		return
	}
	rows := make([]string, h)

	// Header (rows 0-1).
	rows[0] = c.headerRow(w)
	rows[1] = c.sgr(c.theme.Dim, strings.Repeat("─", w))

	bodyTop := 2
	hintRow := h - 1
	boxBot := h - 2
	boxMid := h - 3
	boxTop := h - 4
	bodyBot := boxTop - 1

	// Body window.
	body := c.bodyLines(w)
	bodyH := bodyBot - bodyTop + 1
	if bodyH < 1 {
		bodyH = 1
	}
	maxScroll := maxi(0, len(body)-bodyH)
	if c.scroll > maxScroll {
		c.scroll = maxScroll
	}
	end := len(body) - c.scroll
	start := maxi(0, end-bodyH)
	window := body[start:end]
	pad := bodyH - len(window)
	for i := 0; i < bodyH; i++ {
		r := bodyTop + i
		if i < pad {
			rows[r] = ""
		} else {
			rows[r] = window[i-pad]
		}
	}

	// Slash popup overlays the rows just above the input box.
	c.popRow0 = 0
	if c.popup && len(c.popMatch) > 0 {
		c.overlayPopup(rows, boxTop, w)
	}

	// Input box (rows boxTop..boxBot).
	rows[boxTop] = c.sgr(c.theme.Dim, "╭"+strings.Repeat("─", w-2)+"╮")
	rows[boxMid] = c.inputRow(w)
	rows[boxBot] = c.sgr(c.theme.Dim, "╰"+strings.Repeat("─", w-2)+"╯")

	rows[hintRow] = c.hintRow(w)

	// Modal approval overlays the centre of the body region.
	if c.modal {
		c.overlayModal(rows, bodyTop, bodyBot, w)
	}

	c.sc.BeginFrame()
	for _, r := range rows {
		c.sc.WriteLine(tui.PadTo(r, w))
	}
	c.sc.EndFrame()
}

func (c *chatUI) headerRow(w int) string {
	brand := c.sgr("1;38;5;213", " ✦ Ties ")
	sub := c.sgr(c.theme.Dim, " terminal AI coding agent")
	left := brand + sub
	right := c.sgr(c.theme.Heading, c.model)
	if c.session != "" {
		right += c.sgr(c.theme.Dim, "  "+shortID(c.session))
	}
	right += " "
	gap := w - ui.DisplayWidth(left) - ui.DisplayWidth(right)
	if gap < 1 {
		return tui.Truncate(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func (c *chatUI) bodyLines(w int) []string {
	var lines []string
	indent := "  "
	textW := w - 2
	for i, e := range c.entries {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, c.renderEntry(e, indent, textW)...)
	}
	if c.hasLive {
		if len(c.entries) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, c.renderEntry(chatEntry{kind: ckAssistant, text: c.live}, indent, textW)...)
	}
	if len(lines) == 0 {
		lines = append(lines,
			"",
			"  "+c.sgr(c.theme.Accent, "✦")+c.sgr(c.theme.Dim, "  Welcome to Ties."),
			"  "+c.sgr(c.theme.Dim, "  Ask me to build, debug or explain code."),
			"  "+c.sgr(c.theme.Dim, "  Type a message below, or "+c.sgr(c.theme.Accent, "/")+c.sgr(c.theme.Dim, " for commands.")),
		)
	}
	return lines
}

func (c *chatUI) renderEntry(e chatEntry, indent string, textW int) []string {
	var out []string
	switch e.kind {
	case ckUser:
		out = append(out, c.sgr(c.theme.User, "▌ You"))
		for _, ln := range wrapBlock(e.text, textW) {
			out = append(out, indent+c.sgr(c.theme.User, ln))
		}
	case ckAssistant:
		out = append(out, c.sgr(c.theme.Accent, "✦ Ties"))
		styled := tui.RenderMarkdown(c.theme, c.color, e.text)
		for _, ln := range strings.Split(styled, "\n") {
			for _, w := range tui.Wrap(ln, textW) {
				out = append(out, indent+w)
			}
		}
	case ckTool:
		line := c.sgr(c.theme.Tool, ui.ToolIcon(e.text)+" "+e.text)
		if e.detail != "" {
			line += c.sgr(c.theme.Dim, "  "+e.detail)
		}
		out = append(out, indent+line)
	case ckError:
		for _, ln := range wrapBlock(e.text, textW) {
			out = append(out, indent+c.sgr(c.theme.Error, "✗ "+ln))
		}
	case ckNote:
		for _, ln := range wrapBlock(e.text, textW) {
			out = append(out, indent+c.sgr(c.theme.Dim, ln))
		}
	}
	return out
}

func (c *chatUI) overlayPopup(rows []string, boxTop, w int) {
	n := len(c.popMatch)
	if n > 6 {
		n = 6
	}
	top := boxTop - n
	if top < 2 {
		top = 2
	}
	c.popRow0 = top + 1 // 1-based terminal row of the first item
	for i := 0; i < n; i++ {
		it := c.popMatch[i]
		label := fmt.Sprintf(" %-10s %s ", it.name, it.desc)
		var styled string
		if i == c.popupSel {
			styled = c.sgr("7", tui.PadTo(label, w-4))
		} else {
			styled = c.sgr(c.theme.Tool, tui.PadTo(label, w-4))
		}
		rows[top+i] = "  " + styled
	}
}

func (c *chatUI) inputRow(w int) string {
	fieldW := w - 6 // "│ " + "❯ " + field + " │"
	if fieldW < 1 {
		fieldW = 1
	}
	if c.cur < c.inOff {
		c.inOff = c.cur
	}
	if c.cur >= c.inOff+fieldW {
		c.inOff = c.cur - fieldW + 1
	}
	if c.inOff < 0 {
		c.inOff = 0
	}
	endIdx := c.inOff + fieldW
	if endIdx > len(c.input) {
		endIdx = len(c.input)
	}
	visible := c.input[c.inOff:endIdx]

	var b strings.Builder
	count := 0
	for i, r := range visible {
		idx := c.inOff + i
		if idx == c.cur {
			b.WriteString(c.sgr("7", string(r)))
		} else {
			b.WriteString(string(r))
		}
		count++
	}
	if c.cur >= len(c.input) && count < fieldW {
		b.WriteString(c.sgr("7", " "))
		count++
	}
	field := b.String() + strings.Repeat(" ", maxi(0, fieldW-count))

	prompt := c.sgr(c.theme.Accent, "❯ ")
	return c.sgr(c.theme.Dim, "│ ") + prompt + field + c.sgr(c.theme.Dim, " │")
}

func (c *chatUI) hintRow(w int) string {
	var left string
	if c.working {
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		left = c.sgr(c.theme.Accent, string(frames[c.spin%len(frames)])) + " " + c.sgr(c.theme.Dim, "working…")
	} else {
		left = c.sgr(c.theme.Success, "●") + " " + c.sgr(c.theme.Dim, "ready")
	}
	meta := c.model
	if c.mode != "" {
		meta += " · " + c.mode
	}
	meta += fmt.Sprintf(" · %s in / %s out", group(c.tokIn), group(c.tokOut))
	if c.hasCost {
		meta += fmt.Sprintf(" · $%.4f", c.cost)
	}
	hint := "↵ send   / commands   ctrl+c quit"
	right := c.sgr(c.theme.Dim, meta+"   "+hint)
	gap := w - ui.DisplayWidth(left) - ui.DisplayWidth(right)
	if gap < 2 {
		right = c.sgr(c.theme.Dim, meta)
		gap = w - ui.DisplayWidth(left) - ui.DisplayWidth(right)
	}
	if gap < 1 {
		gap = 1
	}
	return " " + left + strings.Repeat(" ", gap) + right
}

func (c *chatUI) overlayModal(rows []string, top, bot, w int) {
	lines := []string{
		c.sgr(c.theme.Warn, "Permission required"),
		"",
		c.modalText,
		"",
		c.sgr(c.theme.Success, "[y] allow") + "   " + c.sgr(c.theme.Error, "[n] deny"),
	}
	boxW := 0
	for _, l := range lines {
		if d := ui.DisplayWidth(l); d > boxW {
			boxW = d
		}
	}
	boxW += 4
	if boxW > w-2 {
		boxW = w - 2
	}
	left := (w - boxW) / 2
	if left < 0 {
		left = 0
	}
	pad := strings.Repeat(" ", left)
	frame := []string{c.sgr(c.theme.Warn, "╭"+strings.Repeat("─", boxW-2)+"╮")}
	for _, l := range lines {
		frame = append(frame, c.sgr(c.theme.Warn, "│")+" "+tui.PadTo(l, boxW-4)+" "+c.sgr(c.theme.Warn, "│"))
	}
	frame = append(frame, c.sgr(c.theme.Warn, "╰"+strings.Repeat("─", boxW-2)+"╯"))

	startRow := top + ((bot-top+1)-len(frame))/2
	if startRow < top {
		startRow = top
	}
	for i, fl := range frame {
		r := startRow + i
		if r > bot {
			break
		}
		rows[r] = pad + fl
	}
}

// --- helpers ----------------------------------------------------------------

// wrapBlock splits text on newlines then wraps each line to width.
func wrapBlock(text string, width int) []string {
	var out []string
	for _, ln := range strings.Split(text, "\n") {
		out = append(out, tui.Wrap(ln, width)...)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func group(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ",")
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- wiring -----------------------------------------------------------------

// runChatScreen drives the interactive raw-mode chat experience. It returns
// errFallbackChat if the screen cannot be started so the caller can fall back
// to the line-oriented loop.
func (a *app) runChatScreen(ctx context.Context, flags agentFlags, p provider.Provider, model string, sess *session.Session, usage *usageMeter) error {
	sc := screen.New(os.Stdin, os.Stdout)
	if err := sc.Start(); err != nil {
		return errFallbackChat
	}
	defer sc.Stop()

	c := newChatUI(sc, a.ui.Theme(), a.ui.ColorOn(), model, sess.Meta.ID)
	switch {
	case flags.plan:
		c.mode = "plan"
	case flags.tdd:
		c.mode = "tdd"
	case flags.loop:
		c.mode = "loop"
	}

	a.live = c
	defer func() { a.live = nil }()

	ag := a.newAgent(p, model, sess, flags, usage)
	// Route approvals through the in-screen modal unless auto-approving.
	if !flags.yes && !flags.scripting() {
		ag.Approve = func(name, target string) bool { return c.confirm(name, target) }
	}

	if n := len(a.memory); n > 0 {
		c.addNote(fmt.Sprintf("loaded %d context file(s)", n))
	}
	return c.run(ctx, a, ag, model, usage)
}

// chatSlash handles a slash command inside the interactive screen. It returns
// true when the user asked to quit.
func (a *app) chatSlash(c *chatUI, line, model string, usage *usageMeter) bool {
	cmd := strings.Fields(line)[0]
	switch cmd {
	case "/exit", "/quit":
		return true
	case "/help":
		var b strings.Builder
		b.WriteString("commands:\n")
		for _, it := range chatCommands {
			b.WriteString(fmt.Sprintf("  %-9s %s\n", it.name, it.desc))
		}
		c.addNote(strings.TrimRight(b.String(), "\n"))
	case "/tools":
		c.addNote(strings.Join(a.reg.Names(), ", "))
	case "/skills":
		if len(a.skills) == 0 {
			c.addNote("(no skills loaded)")
			break
		}
		var b strings.Builder
		for _, s := range a.skills {
			b.WriteString(s.Name + " — " + s.Description + "\n")
		}
		c.addNote(strings.TrimRight(b.String(), "\n"))
	case "/context":
		if len(a.memory) == 0 {
			c.addNote("(no AGENTS.md / CLAUDE.md / TIES.md found)")
			break
		}
		var b strings.Builder
		for _, d := range a.memory {
			b.WriteString(d.Path + "\n")
		}
		c.addNote(strings.TrimRight(b.String(), "\n"))
	case "/model":
		c.addNote(model)
	case "/usage":
		l := fmt.Sprintf("tokens: %d in / %d out", usage.in, usage.out)
		if cost, ok := pricing.Estimate(model, usage.in, usage.out); ok {
			l += fmt.Sprintf("  ·  est. $%.4f", cost)
		}
		c.addNote(l)
	case "/clear":
		c.mu.Lock()
		c.entries = nil
		c.live = ""
		c.hasLive = false
		c.scroll = 0
		c.renderLocked()
		c.mu.Unlock()
	default:
		c.addNote("unknown command; /help for the list")
	}
	return false
}
