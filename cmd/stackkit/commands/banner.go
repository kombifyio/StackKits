package commands

import (
	"fmt"
	"os"
)

const banner = `
     _             _    _    _ _
 ___| |_ __ _  ___| | _| | _(_) |_
/ __| __/ _` + "`" + ` |/ __| |/ / |/ / | __|
\__ \ || (_| | (__|   <|   <| | |_
|___/\__\__,_|\___|_|\_\_|\_\_|\__|
`

// printBanner displays the stackkit ASCII banner in orange.
//
// Apply never uses it: that command is the long wait after a greeting the
// installer or `init` already printed. STACKKIT_NO_BANNER=1 lets those
// wrappers keep a single greeting for nested CLI calls.
func printBanner() {
	if quiet || humanOutputSuppressed() || os.Getenv("STACKKIT_NO_BANNER") == "1" {
		return
	}
	// 256-color orange (208)
	fmt.Printf("\033[38;5;208m%s\033[0m", banner)
}
