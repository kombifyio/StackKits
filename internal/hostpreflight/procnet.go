package hostpreflight

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// procNetBound reports whether the kernel lists a socket bound to port for the
// transport. It needs no privilege, so it answers the question when a bind
// probe is refused with EACCES (non-root on a privileged port). The second
// result is false when no /proc/net table could be read.
func procNetBound(transport string, port int) (bound, observed bool) {
	return procNetBoundIn("/proc/net", transport, port)
}

func procNetBoundIn(root, transport string, port int) (bound, observed bool) {
	want := fmt.Sprintf(":%04X", port)
	for _, name := range []string{transport, transport + "6"} {
		file, err := os.Open(root + "/" + name)
		if err != nil {
			continue
		}
		observed = true
		scanner := bufio.NewScanner(file)
		scanner.Scan() // header
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || !strings.HasSuffix(strings.ToUpper(fields[1]), want) {
				continue
			}
			// tcp: only LISTEN (0A) holds the port; udp: any bound socket does.
			if transport == "tcp" && fields[3] != "0A" {
				continue
			}
			bound = true
		}
		file.Close()
	}
	return bound, observed
}
