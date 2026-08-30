package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	version = "4"
	defaultClass = "14"
	defaultToken = "00d3cb55146474d7c11235e2940c6950"
	repoOwner = "venver"
	repoName = "dming"
	repoPath = "dms/classes"
	refreshEvery = 90 * time.Second
)

var (
	bg1 = nrgba(0x66, 0x7e, 0xea, 0xff)
	bg2 = nrgba(0x76, 0x4b, 0xa2, 0xff)
	titleWhite = nrgba(0xff, 0xff, 0xff, 0xff)
	titleAccent = nrgba(0xff, 0xd9, 0x3d, 0xff)
	cardWhite = nrgba(0xff, 0xff, 0xff, 0xff)
	nameBase = nrgba(0x2c, 0x3e, 0x50, 0xff)
	shine1 = nrgba(0x34, 0x98, 0xdb, 0xff)
	shine2 = nrgba(0xe7, 0x4c, 0x3c, 0xff)
	textLight = nrgba(0xd9, 0xdc, 0xf4, 0xff)
	textDim = nrgba(0xff, 0xff, 0xff, 0xb3)
	btnBlue = nrgba(0x34, 0x98, 0xdb, 0xff)
	btnRed = nrgba(0xe7, 0x4c, 0x3c, 0xff)
	btnPurple = nrgba(0x8e, 0x44, 0xad, 0xff)
	white = nrgba(0xff, 0xff, 0xff, 0xff)
)

func nrgba(r, g, b, a uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: a} }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var epoch = time.Now()

type rollApp struct {
	th *material.Theme
	w *app.Window
	mu sync.Mutex
	class string
	token string

	names []string
	called map[string]bool
	rolling bool
	current string

	remoteStatus string
	remoteDiff bool

	startBtn *widget.Clickable
	resetBtn *widget.Clickable

	startLift liftState
	resetLift liftState
}

type liftState struct {
	val float32
	last time.Time
}

func main() {
	a := &rollApp{
		class: envOr("ROLLCALL_CLASS", defaultClass),
		token: envOr("GITEE_TOKEN", defaultToken),
		called: map[string]bool{},
	}
	a.th = newTheme()
	a.startBtn = &widget.Clickable{}
	a.resetBtn = &widget.Clickable{}
	a.current = "准备就绪"

	if ns := loadRoster(rosterPath()); len(ns) > 0 {
		a.names = ns
	} else {
		a.remoteStatus = "未找到名单"
	}

	go a.run()
	a.startRemote()
	app.Main()
}

func (a *rollApp) run() {
	w := new(app.Window)
	a.w = w
	w.Option(
		app.Title(fmt.Sprintf("%s班点名", a.class)),
		app.Size(unit.Dp(560), unit.Dp(720)),
		app.MinSize(unit.Dp(420), unit.Dp(540)),
	)
	var ops op.Ops
	for {
		e := w.Event()
		switch ev := e.(type) {
		case app.DestroyEvent:
			return
		case app.FrameEvent:
			gtx := app.NewContext(&ops, ev)
			a.frame(gtx, ev.Now)
			ev.Frame(gtx.Ops)
		}
	}
}

func (a *rollApp) frame(gtx layout.Context, now time.Time) {
	if a.startBtn.Clicked(gtx) {
		a.toggleRolling()
	}
	if a.resetBtn.Clicked(gtx) {
		a.resetRoster()
	}

	a.mu.Lock()
	if a.rolling {
		unc := a.uncalledLocked()
		if len(unc) == 0 {
			a.called = map[string]bool{}
			unc = a.names
		}
		a.current = unc[rand.Intn(len(unc))]
	}
	name := a.current
	rolling := a.rolling
	total := len(a.names)
	calledN := len(a.called)
	remain := total - calledN
	remoteStatus := a.remoteStatus
	a.mu.Unlock()

	a.draw(gtx, name, rolling, total, calledN, remain, remoteStatus, now)

	a.w.Invalidate()
}

func (a *rollApp) toggleRolling() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rolling {
		a.rolling = false
		if a.current != "" && a.current != "准备就绪" {
			if !a.called[a.current] {
				a.called[a.current] = true
			}
		}
		if len(a.names) > 0 && len(a.called) >= len(a.names) {
			a.called = map[string]bool{}
		}
		return
	}
	if len(a.uncalledLocked()) == 0 {
		a.current = "准备就绪"
		return
	}
	a.rolling = true
}

func (a *rollApp) resetRoster() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.called = map[string]bool{}
	a.rolling = false
	a.current = "准备就绪"
	if ns := loadRoster(rosterPath()); len(ns) > 0 {
		a.names = ns
	}
}

func (a *rollApp) uncalledLocked() []string {
	var out []string
	for _, n := range a.names {
		if !a.called[n] {
			out = append(out, n)
		}
	}
	return out
}

func (a *rollApp) draw(gtx layout.Context, name string, rolling bool, total, calledN, remain int, remoteStatus string, now time.Time) layout.Dimensions {
	size := gtx.Constraints.Max
	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	paintLinearGradient(gtx, image.Rectangle{Max: size}, bg1, bg2)

	return layout.Stack{Alignment: layout.Center}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return a.column(gtx, name, rolling, total, calledN, remain, remoteStatus, now)
		}),
	)
}

func (a *rollApp) column(gtx layout.Context, name string, rolling bool, total, calledN, remain int, remoteStatus string, now time.Time) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(24), Bottom: unit.Dp(24), Left: unit.Dp(24), Right: unit.Dp(24)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{
				Axis: layout.Vertical,
				Alignment: layout.Middle,
				Spacing: layout.SpaceBetween,
				Gap: gtx.Dp(unit.Dp(10)),
			}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.versionRow(gtx) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.remoteRow(gtx, remoteStatus) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.titleRow(gtx) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(6)}.Layout(gtx) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.nameCard(gtx, name, now) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(6)}.Layout(gtx) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.controlsRow(gtx, rolling) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Height: unit.Dp(6)}.Layout(gtx) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return a.statsRow(gtx, total, calledN, remain) }),
			)
		},
	)
}

func (a *rollApp) versionRow(gtx layout.Context) layout.Dimensions {
	l := material.Label(a.th, unit.Sp(14), "version "+version)
	l.Color = textLight
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return l.Layout(gtx) })
}

func (a *rollApp) remoteRow(gtx layout.Context, status string) layout.Dimensions {
	if strings.TrimSpace(status) == "" {
		status = "正在获取在线名单…"
	}
	l := material.Label(a.th, unit.Sp(13), status)
	l.Color = textLight
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return l.Layout(gtx) })
}

func (a *rollApp) titleRow(gtx layout.Context) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(a.th, unit.Sp(34), a.class)
				l.Color = titleAccent
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(a.th, unit.Sp(34), "班点名")
				l.Color = titleWhite
				l.Font.Weight = font.Bold
				return l.Layout(gtx)
			}),
		)
	})
}

func (a *rollApp) nameCard(gtx layout.Context, name string, now time.Time) layout.Dimensions {
	max := gtx.Constraints.Max
	cardW := int(float32(max.X) * 0.8)
	if cardW > 760 {
		cardW = 760
	}
	if cardW < 240 {
		cardW = 240
	}
	cardH := int(float32(max.Y) * 0.26)
	if cardH > 200 {
		cardH = 200
	}
	if cardH < 120 {
		cardH = 120
	}
	gtx.Constraints = layout.Exact(image.Pt(cardW, cardH))
	cardR := image.Rect(0, 0, cardW, cardH)
	radius := gtx.Dp(unit.Dp(26))

	a.drawCardShadow(gtx, cardR, radius)

	defer clip.UniformRRect(cardR, radius).Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, cardWhite)
	a.drawShineName(gtx, name, now)

	return layout.Dimensions{Size: image.Pt(cardW, cardH)}
}

func (a *rollApp) drawCardShadow(gtx layout.Context, cardR image.Rectangle, radius int) {
	type layer struct {
		off, grow int
		alpha uint8
	}
	layers := []layer{
		{off: 5, grow: 0, alpha: 0x22},
		{off: 9, grow: 5, alpha: 0x14},
		{off: 13, grow: 10, alpha: 0x0c},
	}
	for _, l := range layers {
		r := image.Rect(
			cardR.Min.X-l.grow,
			cardR.Min.Y+l.off-l.grow/2,
			cardR.Max.X+l.grow,
			cardR.Max.Y+l.off+l.grow/2,
		)
		st := clip.UniformRRect(r, radius+l.grow/2).Push(gtx.Ops)
		paint.Fill(gtx.Ops, color.NRGBA{R: 0x1a, G: 0x1a, B: 0x2e, A: l.alpha})
		st.Pop()
	}
}

func (a *rollApp) drawShineName(gtx layout.Context, name string, now time.Time) {
	if strings.TrimSpace(name) == "" {
		name = "准备就绪"
	}
	fontFace := font.Font{Weight: font.Bold}
	textSize := unit.Sp(58)

	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		measureMacro := op.Record(gtx.Ops)
		paint.ColorOp{Color: cardWhite}.Add(gtx.Ops)
		dummy := measureMacro.Stop()
		mMacro := op.Record(gtx.Ops)
		dims := widget.Label{Alignment: text.Start, MaxLines: 1}.Layout(gtx, a.th.Shaper, fontFace, textSize, name, dummy)
		mMacro.Stop()
		textW, textH := dims.Size.X, dims.Size.Y
		if textW <= 0 {
			return dims
		}

		baseMacro := op.Record(gtx.Ops)
		paint.ColorOp{Color: nameBase}.Add(gtx.Ops)
		base := baseMacro.Stop()
		widget.Label{Alignment: text.Start, MaxLines: 1}.Layout(gtx, a.th.Shaper, fontFace, textSize, name, base)

		bandW := int(float32(textW)*0.35) + 10
		span := textW - bandW
		if span < 0 {
			span = textW
		}
		t := scrollPhase(now)
		bx0 := int(float32(span) * t)
		bx1 := bx0 + bandW
		band := image.Rect(bx0, 0, bx1, textH)
		if band.Dx() > 0 {
			defer clip.Rect(band).Push(gtx.Ops).Pop()
			shineMacro := op.Record(gtx.Ops)
			paint.LinearGradientOp{
				Stop1: f32.Point{X: float32(band.Min.X), Y: float32(band.Min.Y)},
				Color1: shine1,
				Stop2: f32.Point{X: float32(band.Max.X), Y: float32(band.Min.Y)},
				Color2: shine2,
			}.Add(gtx.Ops)
			shine := shineMacro.Stop()
			widget.Label{Alignment: text.Start, MaxLines: 1}.Layout(gtx, a.th.Shaper, fontFace, textSize, name, shine)
		}
		return dims
	})
}

func (a *rollApp) controlsRow(gtx layout.Context, rolling bool) layout.Dimensions {
	startText := "开始点名"
	startBg := btnBlue
	if rolling {
		startText = "停止点名"
		startBg = btnRed
	}
	return layout.Flex{
		Axis: layout.Horizontal,
		Alignment: layout.Middle,
		Spacing: layout.SpaceBetween,
		Gap: gtx.Dp(unit.Dp(18)),
	}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.button(gtx, a.startBtn, startText, startBg, &a.startLift)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.button(gtx, a.resetBtn, "手动重置", btnPurple, &a.resetLift)
		}),
	)
}

func (a *rollApp) button(gtx layout.Context, c *widget.Clickable, txt string, bg color.NRGBA, ls *liftState) layout.Dimensions {
	target := float32(0)
	if c.Hovered() {
		target = -3
	}
	now := gtx.Now
	if ls.last.IsZero() {
		ls.last = now
	}
	dt := now.Sub(ls.last).Seconds()
	if dt <= 0 || dt > 0.1 {
		dt = 1.0 / 60.0
	}
	ls.last = now
	ls.val = easeLift(ls.val, target, dt)
	defer op.Offset(image.Pt(0, int(ls.val))).Push(gtx.Ops).Pop()

	b := material.Button(a.th, c, txt)
	b.Background = bg
	b.Color = white
	b.CornerRadius = unit.Dp(14)
	b.Inset = layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(12), Left: unit.Dp(28), Right: unit.Dp(28)}
	return b.Layout(gtx)
}

func easeLift(cur, target float32, dt float64) float32 {
	const speed = 14.0
	f := 1 - math.Exp(-speed*dt)
	return cur + (target-cur)*float32(f)
}

func (a *rollApp) statsRow(gtx layout.Context, total, calledN, remain int) layout.Dimensions {
	txt := fmt.Sprintf("当前共 %d 人 | 已点 %d 人 | 剩余 %d 人", total, calledN, remain)
	l := material.Label(a.th, unit.Sp(18), txt)
	l.Color = white
	l.Font.Weight = font.Medium
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return l.Layout(gtx) })
}

func scrollPhase(now time.Time) float32 {
	const period = 3.0 
	t := now.Sub(epoch).Seconds()
	if t < 0 {
		t = 0
	}
	return float32(math.Mod(t/period, 1.0))
}

func (a *rollApp) startRemote() {
	go func() {
		for {
			names, err := fetchRemote(a.class, a.token)
			a.mu.Lock()
			if err != nil {
				a.remoteDiff = false
				a.remoteStatus = "在线名单获取失败（网络/令牌）"
			} else {
				a.remoteStatus = fmt.Sprintf("在线名单 %d 人", len(names))
				if sameSet(names, a.names) {
					a.remoteDiff = false
					a.remoteStatus += "，与本地一致"
				} else {
					a.remoteDiff = true
					a.remoteStatus += "，与本地不一致"
				}
			}
			a.mu.Unlock()
			time.Sleep(refreshEvery)
		}
	}()
}

type giteeContent struct {
	Content string `json:"content"`
}

func fetchRemote(class, token string) ([]string, error) {
	u := fmt.Sprintf("https://gitee.com/api/v5/repos/%s/%s/contents/%s/%s.txt?access_token=%s",
		repoOwner, repoName, repoPath, class, token)
	client := &http.Client{Timeout: 12 * time.Second}
	res, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var out giteeContent
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Content == "" {
		return nil, fmt.Errorf("empty content")
	}
	b64 := strings.Join(strings.Fields(out.Content), "")
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	return parseRoster(string(raw)), nil
}

func rosterPath() string {
	if p := os.Getenv("ROLLCALL_ROSTER"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "p.txt")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		cand := filepath.Join(cwd, "p.txt")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return "p.txt"
}

func loadRoster(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseRoster(string(data))
}

func parseRoster(s string) []string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	seen := map[string]bool{}
	var out []string
	for _, ln := range lines {
		n := strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}

func newTheme() *material.Theme {
	th := material.NewTheme()
	faces := loadCJKFonts()
	faces = append(gofont.Regular(), faces...)
	if len(faces) > 0 {
		th.Shaper = text.NewShaper(text.WithCollection(faces))
	}
	th.TextSize = unit.Sp(18)
	return th
}

func loadCJKFonts() []font.FontFace {
	paths := []string{
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJKsc-Regular.otf",
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
		"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
		"/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc",
		"/System/Library/Fonts/PingFang.ttc",
		"/System/Library/Fonts/Hiragino Sans GB.ttc",
		"",
	}
	paths = append(paths,
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\msyh.ttf`,
		`C:\Windows\Fonts\simsun.ttc`,
		`C:\Windows\Fonts\simhei.ttf`,
	)
	for _, p := range paths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		faces, err := opentype.ParseCollection(data)
		if err != nil {
			continue
		}
		if len(faces) > 0 {
			return faces
		}
	}
	return nil
}

func paintLinearGradient(gtx layout.Context, r image.Rectangle, c1, c2 color.NRGBA) {
	m := op.Record(gtx.Ops)
	paint.LinearGradientOp{
		Stop1: f32.Point{X: float32(r.Min.X), Y: float32(r.Min.Y)},
		Color1: c1,
		Stop2: f32.Point{X: float32(r.Max.X), Y: float32(r.Max.Y)},
		Color2: c2,
	}.Add(gtx.Ops)
	brush := m.Stop()
	brush.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}
