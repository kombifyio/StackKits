package commands

import (
	"io"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
	"github.com/olekukonko/tablewriter/tw"
)

func writeServiceStatusTable(out io.Writer, rows [][]string) error {
	table := tablewriter.NewTable(out,
		tablewriter.WithRenderer(renderer.NewBlueprint(tw.Rendition{
			Borders: tw.BorderNone,
			Symbols: tw.NewSymbols(tw.StyleASCII),
		})),
		tablewriter.WithHeaderAutoFormat(tw.Off),
	)
	// Preserve the existing bold header and ANSI status/health cells.
	table.Header("\x1b[1mSERVICE\x1b[0m", "\x1b[1mSTATUS\x1b[0m", "\x1b[1mHEALTH\x1b[0m", "\x1b[1mCONTAINER\x1b[0m", "\x1b[1mURL\x1b[0m")
	for _, row := range rows {
		if err := table.Append(row); err != nil {
			return err
		}
	}
	return table.Render()
}
