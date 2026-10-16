package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/mtalavi/coolify-mirror/internal/engine"
)

// RepoURL is the project's home page.
const RepoURL = engine.RepoURL

var logo = []string{
	"█▀▀ █▀█ █▀█ █   █ █▀▀ █▄█   █▀▄▀█ █ █▀█ █▀█ █▀█ █▀█",
	"█▄▄ █▄█ █▄█ █▄▄ █ █▀   █    █ ▀ █ █ █▀▄ █▀▄ █▄█ █▀▄",
}

// Gradient stops of the logo (violet → blue → cyan).
var gradient = [][3]float64{{167, 139, 250}, {96, 165, 250}, {34, 211, 238}}

func gradientAt(t float64) lipgloss.Color {
	t -= float64(int(t))
	if t < 0 {
		t++
	}
	// Ping-pong so the sweep has no hard edge.
	t *= 2
	if t > 1 {
		t = 2 - t
	}
	seg := t * float64(len(gradient)-1)
	i := int(seg)
	if i >= len(gradient)-1 {
		i = len(gradient) - 2
	}
	f := seg - float64(i)
	a, b := gradient[i], gradient[i+1]
	c := func(k int) int { return int(a[k] + (b[k]-a[k])*f) }
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", c(0), c(1), c(2)))
}

func paintLogo(shift float64, reveal int) string {
	var sb strings.Builder
	for _, line := range logo {
		r := []rune(line)
		sb.WriteString("  ")
		for i, ch := range r {
			if i >= reveal {
				sb.WriteRune(' ')
				continue
			}
			if ch == ' ' {
				sb.WriteRune(ch)
				continue
			}
			sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(gradientAt(float64(i)/float64(len(r))*0.8 + shift)).Render(string(ch)))
		}
		sb.WriteRune('\n')
	}
	return sb.String()
}

// link renders an OSC 8 hyperlink (plain text in terminals without support).
func link(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// Banner prints the logo (animated on an interactive terminal) and the
// project information.
func Banner() {
	interactive := term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	width := len([]rune(logo[0]))
	fmt.Println()
	if interactive && termWidth() >= width+4 {
		const frames = 18
		up := fmt.Sprintf("\x1b[%dA", len(logo))
		fmt.Print("\x1b[?25l")
		for f := 0; f <= frames; f++ {
			if f > 0 {
				fmt.Print(up)
			}
			fmt.Print(paintLogo(float64(f)/frames*0.6, width*f/frames))
			time.Sleep(28 * time.Millisecond)
		}
		fmt.Print("\x1b[?25h")
	} else if termWidth() >= width+4 {
		fmt.Print(paintLogo(0, width))
	} else {
		fmt.Println(sTitle.Render("  ◆ Coolify Mirror"))
	}

	pill := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#0B1020")).Background(gradientAt(0.25)).Padding(0, 1)
	fmt.Println()
	fmt.Println("  " + pill.Render("v"+engine.Version) + "  " + sBold.Render("Move Coolify apps between servers — exactly as they run"))
	feats := []string{"selective or full", "data + databases", "no rebuild", "verified on arrival", "rollback"}
	var fs []string
	for i, f := range feats {
		fs = append(fs, lipgloss.NewStyle().Foreground(gradientAt(float64(i)/float64(len(feats))*0.5)).Render("● ")+sMuted.Render(f))
	}
	fmt.Println("  " + strings.Join(fs, "  "))
	fmt.Println("  " + sMuted.Render("◆ guide ") + sAccent.Render(link(engine.SiteURL, strings.TrimPrefix(engine.SiteURL, "https://"))) +
		sMuted.Render("  ·  ★ ") + sAccent.Render(link(RepoURL, strings.TrimPrefix(RepoURL, "https://"))) + sMuted.Render("  ·  MIT"))
}
