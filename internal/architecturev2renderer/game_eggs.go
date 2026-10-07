package architecturev2renderer

import _ "embed"

// The official Pterodactyl Panel v1.15.1 Minecraft Java Eggs (Vanilla and
// Paper), which the Pterodactyl Panel seeds itself.
const (
	GameVanillaMinecraftEggSHA256 = "20dc827bb5f692e3b0a0315cce8feefbb32d44d0a658bafe9c100384d9904c8a"
	GamePaperEggSHA256            = "dbe9c1e4cc65b2cc67d4d422078c3526dacd6c09958b57bf7717d7173023d5eb"
)

var (
	//go:embed assets/game/egg-vanilla-minecraft.json
	gameVanillaMinecraftEgg string
	//go:embed assets/game/egg-paper.json
	gamePaperEgg string
)

// gameEggs are every curated Egg, pinned by content digest, for the
// platforms that seed none (ADR-0048): both Pterodactyl-format Eggs import
// unchanged into Calagopus and Pelican.
var gameEggs = []struct{ Path, Body, SHA256 string }{
	{Path: "/stackkit/eggs/minecraft-java-vanilla.json", Body: gameVanillaMinecraftEgg, SHA256: GameVanillaMinecraftEggSHA256},
	{Path: "/stackkit/eggs/minecraft-java-paper.json", Body: gamePaperEgg, SHA256: GamePaperEggSHA256},
	{Path: "/stackkit/eggs/minecraft-bedrock.json", Body: pterodactylBedrockEgg, SHA256: PterodactylBedrockEggSHA256},
	{Path: "/stackkit/eggs/terraria-vanilla.json", Body: pterodactylTerrariaEgg, SHA256: PterodactylTerrariaEggSHA256},
	{Path: "/stackkit/eggs/valheim-vanilla.json", Body: pterodactylValheimEgg, SHA256: PterodactylValheimEggSHA256},
}
