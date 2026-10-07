package appsetup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GameProfile is one curated game-server recipe (ADR-0043). Images are pinned
// by digest; secure defaults are game-specific and never copied between games.
type GameProfile struct {
	ID          string
	DisplayName string
	DefaultName string
	EggName     string
	NestName    string
	DockerImage string
	Port        int
	// ExtraPorts are further node ports the game needs (for example a query port).
	ExtraPorts []int
	Protocol   string // transport of Port: "tcp" or "udp"
	MemoryMB   int
	DiskMB     int
	CPUPercent int
	// Environment overlays the Egg's own variable defaults.
	Environment func(name, password string) map[string]string
	// PropertiesFile receives Properties before first start; empty when the
	// Egg renders its configuration from the environment.
	PropertiesFile string
	Properties     map[string]string
	// NameProperty carries the world name into PropertiesFile.
	NameProperty string
	// Access is "allow-list" (named accounts) or "password" (owner-chosen
	// join password shared with friends).
	Access string
	// AllowCommand is the console command that admits one player.
	AllowCommand string
	// AllowListFile is where the server records admitted players.
	AllowListFile string
	// WritesEULA reports whether the server reads eula.txt.
	WritesEULA bool
	// AccountHint names the account kind players are admitted by.
	AccountHint string
	// PasswordFile is where the Egg renders the join password as a
	// "password=" line, read back after start.
	PasswordFile string
	// Probe performs the first steps of a real client join on the node with
	// the game's own protocol; password is the owner's join password, if any.
	Probe     func(host string, port int, password string) (string, error)
	ProbePort int
}

var minecraftJavaProperties = map[string]string{
	"online-mode": "true", "white-list": "true", "enforce-whitelist": "true", "enable-rcon": "false",
	"enable-query": "false", "difficulty": "normal", "max-players": "10", "server-port": "25565",
}

// GameProfiles is the curated catalog. Adding a game is a reviewed change:
// Egg source, image digest, secure defaults, ports, resources and a probe.
var GameProfiles = map[string]GameProfile{
	"minecraft-java": {
		ID: "minecraft-java", DisplayName: "Minecraft Java (Vanilla)", DefaultName: "Family Survival",
		EggName: "Vanilla Minecraft", NestName: "Minecraft",
		DockerImage: "ghcr.io/pterodactyl/yolks:java_25@sha256:bc301e4696f5c4fc1bb21214f960ebdfd0ae05ffceaf5cd68511bf6e493757c3",
		Port:        25565, Protocol: "tcp", MemoryMB: 2048, DiskMB: 10240, CPUPercent: 200,
		Environment: func(string, string) map[string]string {
			return map[string]string{"SERVER_JARFILE": "server.jar", "VANILLA_VERSION": "latest"}
		},
		PropertiesFile: "/server.properties", Properties: minecraftJavaProperties, NameProperty: "motd",
		Access: "allow-list", AllowCommand: "whitelist add %s", AllowListFile: "/whitelist.json", WritesEULA: true,
		AccountHint: "Minecraft account names", Probe: javaJoinCheck, ProbePort: 25565,
	},
	// Paper is the performance fork most family servers run; it keeps the
	// vanilla protocol, so Java clients join without mods.
	"minecraft-paper": {
		ID: "minecraft-paper", DisplayName: "Minecraft Java (Paper)", DefaultName: "Family Paper",
		EggName: "Paper", NestName: "Minecraft",
		DockerImage: "ghcr.io/pterodactyl/yolks:java_25@sha256:bc301e4696f5c4fc1bb21214f960ebdfd0ae05ffceaf5cd68511bf6e493757c3",
		Port:        25566, Protocol: "tcp", MemoryMB: 3072, DiskMB: 10240, CPUPercent: 200,
		Environment: func(string, string) map[string]string {
			return map[string]string{"SERVER_JARFILE": "server.jar", "MINECRAFT_VERSION": "latest", "BUILD_NUMBER": "latest"}
		},
		PropertiesFile: "/server.properties", Properties: withProperty(minecraftJavaProperties, "server-port", "25566"), NameProperty: "motd",
		Access: "allow-list", AllowCommand: "whitelist add %s", AllowListFile: "/whitelist.json", WritesEULA: true,
		AccountHint: "Minecraft account names", Probe: javaJoinCheck, ProbePort: 25566,
	},
	"minecraft-bedrock": {
		ID: "minecraft-bedrock", DisplayName: "Minecraft Bedrock", DefaultName: "Family Bedrock",
		EggName: "Vanilla Bedrock", NestName: "Minecraft",
		DockerImage: "ghcr.io/ptero-eggs/yolks:debian@sha256:d7b83c285632be9c9dd03b909378200c4f4c664035d72ad4b5b694af03dc75ac",
		Port:        19132, Protocol: "udp", MemoryMB: 1536, DiskMB: 8192, CPUPercent: 200,
		Environment: func(name, _ string) map[string]string {
			return map[string]string{
				"BEDROCK_VERSION": "latest", "LD_LIBRARY_PATH": ".", "SERVERNAME": name,
				"GAMEMODE": "survival", "DIFFICULTY": "normal", "CHEATS": "false",
			}
		},
		// Bedrock 1.26 defaults to NetherNet; direct IP joins need RakNet.
		PropertiesFile: "/server.properties",
		Properties: map[string]string{
			"online-mode": "true", "allow-list": "true", "transport": "raknet", "server-port": "19132",
			"max-players": "10", "default-player-permission-level": "member",
		},
		NameProperty: "server-name",
		Access:       "allow-list", AllowCommand: "allowlist add %s", AllowListFile: "/allowlist.json",
		AccountHint: "Xbox gamertags", Probe: bedrockJoinCheck, ProbePort: 19132,
	},
	// Terraria has no account-based allow list; a join password is required.
	"terraria": {
		ID: "terraria", DisplayName: "Terraria", DefaultName: "Family Terraria",
		EggName: "Terraria Vanilla", NestName: "Terraria",
		DockerImage: "ghcr.io/ptero-eggs/yolks:debian@sha256:d7b83c285632be9c9dd03b909378200c4f4c664035d72ad4b5b694af03dc75ac",
		Port:        7777, Protocol: "tcp", MemoryMB: 1536, DiskMB: 4096, CPUPercent: 200,
		Environment: func(name, password string) map[string]string {
			return map[string]string{
				"TERRARIA_VERSION": "latest", "WORLD_NAME": worldSlug(name, 20), "MAX_PLAYERS": "8",
				"WORLD_SIZE": "2", "WORLD_DIFFICULTY": "0", "SERVER_MOTD": name, "PASSWORD": password,
			}
		},
		Access: "password", PasswordFile: "/serverconfig.txt", Probe: terrariaHandshake, ProbePort: 7777,
	},
	// Valheim stays off the public server list and off crossplay relays;
	// friends join by address and password. Port+1 is Steam's query port.
	"valheim": {
		ID: "valheim", DisplayName: "Valheim", DefaultName: "Family Valheim",
		EggName: "Valheim", NestName: "Valheim",
		DockerImage: "ghcr.io/ptero-eggs/games:valheim@sha256:5ade5bcfc0c00ad26fd1bac8ff35b6e5e48d8c6cb70ddb5ba72f448520956e24",
		Port:        2456, ExtraPorts: []int{2457}, Protocol: "udp", MemoryMB: 4096, DiskMB: 10240, CPUPercent: 300,
		Environment: func(name, password string) map[string]string {
			return map[string]string{
				"SERVER_NAME": name, "WORLD": worldSlug(name, 20), "PASSWORD": password,
				"PUBLIC_SERVER": "0", "ENABLE_CROSSPLAY": "0", "AUTO_UPDATE": "1",
			}
		},
		Access: "password", Probe: steamNetworkingChallenge, ProbePort: 2456,
	},
}

func withProperty(base map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	out[key] = value
	return out
}

// GameProfileIDs lists the curated profiles in a stable order.
func GameProfileIDs() []string {
	return []string{"minecraft-java", "minecraft-paper", "minecraft-bedrock", "terraria", "valheim"}
}

var gamePlayerName = regexp.MustCompile(`^[A-Za-z0-9_ .-]{1,32}$`)

// mergeServerProperties sets the curated keys and keeps every other line.
func mergeServerProperties(current string, values map[string]string) string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(current, "\r\n", "\n"), "\n") {
		key, _, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if found && !strings.HasPrefix(key, "#") {
			if value, ok := values[key]; ok {
				out = append(out, key+"="+value)
				seen[key] = true
				continue
			}
		}
		if line != "" || len(out) > 0 {
			out = append(out, line)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sortStrings(keys)
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func slugGameName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug == "" {
		slug = "world"
	}
	return slug
}

// observeGameProtocol asks the published node port with the game's own
// discovery protocol, as a client on the node would.
func observeGameProtocol(ctx context.Context, profile GameProfile, password string, limit time.Duration) (string, error) {
	deadline := time.Now().Add(limit)
	var lastErr error
	for time.Now().Before(deadline) {
		answer, err := profile.Probe("127.0.0.1", profile.ProbePort, password)
		if err == nil {
			return answer, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return "", fmt.Errorf("game port %d did not answer its discovery protocol: %w", profile.ProbePort, lastErr)
}

// javaJoinCheck reads the server status, then starts a login as a joining
// player: an online-mode server must answer with an encryption request, the
// step that hands the join to Microsoft account authentication.
func javaJoinCheck(host string, port int, _ string) (string, error) {
	version, protocol, err := javaServerListPing(host, port)
	if err != nil {
		return "", err
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	handshake := append(minecraftVarint(0), minecraftVarint(protocol)...)
	handshake = append(handshake, minecraftVarint(len(host))...)
	handshake = append(handshake, host...)
	handshake = binary.BigEndian.AppendUint16(handshake, uint16(port))
	handshake = append(handshake, minecraftVarint(2)...)
	name := "StackKitsProbe"
	login := append(minecraftVarint(0), minecraftVarint(len(name))...)
	login = append(login, name...)
	login = append(login, make([]byte, 16)...)
	packet := append(minecraftVarint(len(handshake)), handshake...)
	packet = append(packet, minecraftVarint(len(login))...)
	packet = append(packet, login...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	reader := bufio.NewReader(conn)
	if _, err := binary.ReadUvarint(reader); err != nil {
		return "", err
	}
	id, err := binary.ReadUvarint(reader)
	if err != nil {
		return "", err
	}
	switch id {
	case 0x01:
		return "minecraft-java " + version + " online-mode login", nil
	case 0x02, 0x03:
		return "", errors.New("the Minecraft server admitted an unauthenticated login; online mode is off")
	}
	return "", fmt.Errorf("unexpected Minecraft login answer 0x%02x", id)
}

func minecraftVarint(n int) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func javaServerListPing(host string, port int) (string, int, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	varint := func(n int) []byte {
		var out []byte
		for {
			b := byte(n & 0x7f)
			n >>= 7
			if n != 0 {
				b |= 0x80
			}
			out = append(out, b)
			if n == 0 {
				return out
			}
		}
	}
	handshake := append(varint(0), varint(767)...)
	handshake = append(handshake, varint(len(host))...)
	handshake = append(handshake, host...)
	handshake = binary.BigEndian.AppendUint16(handshake, uint16(port))
	handshake = append(handshake, varint(1)...)
	packet := append(varint(len(handshake)), handshake...)
	packet = append(packet, varint(1)...)
	packet = append(packet, varint(0)...)
	if _, err := conn.Write(packet); err != nil {
		return "", 0, err
	}
	readVarint := func() (int, error) {
		value := 0
		for i := 0; i < 5; i++ {
			var b [1]byte
			if _, err := io.ReadFull(conn, b[:]); err != nil {
				return 0, err
			}
			value |= int(b[0]&0x7f) << (7 * i)
			if b[0]&0x80 == 0 {
				return value, nil
			}
		}
		return 0, errors.New("invalid varint")
	}
	if _, err := readVarint(); err != nil {
		return "", 0, err
	}
	if _, err := readVarint(); err != nil {
		return "", 0, err
	}
	length, err := readVarint()
	if err != nil || length <= 0 || length > 1<<20 {
		return "", 0, errors.New("invalid status length")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return "", 0, err
	}
	var status struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return "", 0, err
	}
	return status.Version.Name, status.Version.Protocol, nil
}

func bedrockRakNetPing(host string, port int) (string, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	magic := rakNetMagic
	guid := make([]byte, 8)
	_, _ = rand.Read(guid)
	packet := []byte{0x01}
	packet = binary.BigEndian.AppendUint64(packet, uint64(time.Now().UnixMilli()))
	packet = append(packet, magic...)
	packet = append(packet, guid...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", err
	}
	if n < 35 || buffer[0] != 0x1c {
		return "", errors.New("unexpected RakNet answer")
	}
	length := int(binary.BigEndian.Uint16(buffer[33:35]))
	if 35+length > n {
		return "", errors.New("truncated RakNet answer")
	}
	fields := strings.Split(string(buffer[35:35+length]), ";")
	if len(fields) < 4 || fields[0] != "MCPE" {
		return "", errors.New("RakNet answer is not a Bedrock server")
	}
	return "minecraft-bedrock " + fields[3], nil
}

// worldSlug is a filesystem-safe world name within the game's length limit.
func worldSlug(name string, limit int) string {
	slug := slugGameName(name)
	if len(slug) > limit {
		slug = strings.TrimRight(slug[:limit], "-")
	}
	return slug
}

// validateGamePassword admits a join password friends can type: 8 to 20
// printable characters without quotes, not contained in the server name
// (Valheim refuses such passwords).
func validateGamePassword(password, name string) error {
	if len(password) < 8 || len(password) > 20 {
		return errors.New("a join password of 8 to 20 characters is required (password)")
	}
	for _, r := range password {
		if r < 0x21 || r > 0x7e || r == '"' || r == '\'' || r == '\\' {
			return errors.New("the join password may only use printable ASCII characters without spaces, quotes or backslashes")
		}
	}
	if strings.Contains(strings.ToLower(name), strings.ToLower(password)) {
		return errors.New("the join password must not be part of the server name")
	}
	return nil
}

// terrariaHandshake sends a client hello and accepts only the password
// challenge: a Terraria server that admits a client without a password is
// not a curated server.
func terrariaHandshake(host string, port int, password string) (string, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	version := "Terraria326" // 1.4.5 protocol
	payload := append([]byte{1, byte(len(version))}, version...)
	packet := binary.LittleEndian.AppendUint16(nil, uint16(len(payload)+2))
	packet = append(packet, payload...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	header := make([]byte, 3)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	switch header[2] {
	case 37:
	case 2:
		// The server rejected the probe's client version, which still proves
		// a Terraria server answers; setup reads the password setting back.
		return "terraria answered (client protocol differs)", nil
	case 3:
		return "", errors.New("the Terraria server admitted a client without a password")
	default:
		return "", fmt.Errorf("unexpected Terraria answer type %d", header[2])
	}
	if length := int(binary.LittleEndian.Uint16(header[:2])); length > 3 {
		if _, err := io.CopyN(io.Discard, conn, int64(length-3)); err != nil {
			return "", err
		}
	}
	// Answer the challenge as a joining player would; the server assigns a
	// player slot only for the owner's password.
	if len(password) > 127 {
		return "", errors.New("join password is too long")
	}
	answer := append([]byte{38, byte(len(password))}, password...)
	if _, err := conn.Write(append(binary.LittleEndian.AppendUint16(nil, uint16(len(answer)+2)), answer...)); err != nil {
		return "", err
	}
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	if header[2] != 3 {
		return "", fmt.Errorf("the Terraria server did not admit the join password (answer type %d)", header[2])
	}
	return "terraria join accepted with password", nil
}

// steamNetworkingChallenge sends a Steam networking sockets challenge request
// to the game port. An unlisted Valheim server does not answer Steam A2S
// queries, but it answers this handshake with a reply echoing our connection.
func steamNetworkingChallenge(host string, port int, _ string) (string, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	connectionID := make([]byte, 4)
	_, _ = rand.Read(connectionID)
	connectionID[0] |= 1
	// CMsgSteamSockets_UDP_ChallengeRequest: connection_id (fixed32, field 1),
	// my_timestamp (fixed64, field 3), protocol_version (varint, field 4).
	message := append([]byte{0x0d}, connectionID...)
	message = binary.LittleEndian.AppendUint64(append(message, 0x19), uint64(time.Now().UnixMicro()))
	message = append(message, 0x20, 11)
	packet := binary.LittleEndian.AppendUint16([]byte{32}, uint16(len(message)))
	packet = append(packet, message...)
	packet = append(packet, make([]byte, 512-len(packet))...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", err
	}
	if n < 6 || buffer[0] != 33 || buffer[1] != 0x0d || !bytes.Equal(buffer[2:6], connectionID) {
		return "", errors.New("the game port did not answer the Steam networking challenge")
	}
	return "steam-networking challenge-reply", nil
}

var rakNetMagic = []byte{0x00, 0xff, 0xff, 0x00, 0xfe, 0xfe, 0xfe, 0xfe, 0xfd, 0xfd, 0xfd, 0xfd, 0x12, 0x34, 0x56, 0x78}

// bedrockJoinCheck pings the server, then opens a RakNet connection as a
// joining client would; the server must offer its MTU in an open-connection
// reply.
func bedrockJoinCheck(host string, port int, _ string) (string, error) {
	answer, err := bedrockRakNetPing(host, port)
	if err != nil {
		return "", err
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	packet := append([]byte{0x05}, rakNetMagic...)
	packet = append(packet, 11)
	packet = append(packet, make([]byte, 1400-len(packet))...)
	if _, err := conn.Write(packet); err != nil {
		return "", err
	}
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", err
	}
	if n < 1 || buffer[0] != 0x06 {
		return "", errors.New("the Bedrock server did not accept a RakNet connection")
	}
	return answer + " connection accepted", nil
}
