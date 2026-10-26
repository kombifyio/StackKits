package hostpreflight

import (
	"bufio"
	"encoding/binary"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// procNetBound checks the exact binding when a privileged bind probe returns
// EACCES. Wildcard sockets can overlap concrete addresses. Free requires a
// complete IPv4 and IPv6 inventory; an unreadable table cannot prove absence.
func procNetBound(transport string, address netip.Addr, port int) (bound, observed bool) {
	complete := true
	for _, name := range []string{transport, transport + "6"} {
		file, err := os.Open("/proc/net/" + name)
		if err != nil {
			complete = false
			continue
		}
		tableBound, tableObserved := procNetTableBound(file, transport, address, port)
		file.Close()
		if tableBound {
			return true, true
		}
		complete = complete && tableObserved
	}
	return false, complete
}

func procNetTableBound(file *os.File, transport string, wanted netip.Addr, port int) (bool, bool) {
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), "local_address") {
		return false, false
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			return false, false
		}
		// TCP requires LISTEN; UDP holds a local binding in any state.
		if transport == "tcp" && fields[3] != "0A" {
			continue
		}
		hexAddress, hexPort, ok := strings.Cut(fields[1], ":")
		boundPort, err := strconv.ParseUint(hexPort, 16, 16)
		boundAddress, addressOK := procNetAddress(hexAddress)
		if !ok || len(hexPort) != 4 || err != nil || !addressOK {
			return false, false
		}
		if int(boundPort) != port {
			continue
		}
		// /proc does not identify a zone. Treat matching scoped addresses
		// conservatively, including IPv6 wildcard sockets that may be dual stack.
		wanted = wanted.Unmap().WithZone("")
		if boundAddress == wanted || (boundAddress.Is4() == wanted.Is4() && (boundAddress.IsUnspecified() || wanted.IsUnspecified())) || boundAddress == netip.IPv6Unspecified() || wanted == netip.IPv6Unspecified() {
			return true, true
		}
	}
	return false, scanner.Err() == nil
}

// The kernel prints each native-endian 32-bit address word as hexadecimal.
func procNetAddress(value string) (netip.Addr, bool) {
	if len(value) != 8 && len(value) != 32 {
		return netip.Addr{}, false
	}
	var bytes [16]byte
	for offset := 0; offset < len(value); offset += 8 {
		word, err := strconv.ParseUint(value[offset:offset+8], 16, 32)
		if err != nil {
			return netip.Addr{}, false
		}
		binary.NativeEndian.PutUint32(bytes[offset/2:offset/2+4], uint32(word))
	}
	address, ok := netip.AddrFromSlice(bytes[:len(value)/2])
	if !ok {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}
