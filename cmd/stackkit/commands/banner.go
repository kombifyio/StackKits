package commands

import "fmt"

const banner = `
     _             _    _    _ _
 ___| |_ __ _  ___| | _| | _(_) |_
/ __| __/ _` + "`" + ` |/ __| |/ / |/ / | __|
\__ \ || (_| | (__|   <|   <| | |_
|___/\__\__,_|\___|_|\_\_|\_\_|\__|
`

// printBanner displays the stackkit ASCII banner in orange.
func printBanner() {
	if quiet {
		return
	}
	// 256-color orange (208)
	fmt.Printf("\033[38;5;208m%s\033[0m", banner)
}
