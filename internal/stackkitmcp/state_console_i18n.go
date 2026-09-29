package stackkitmcp

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed assets/state-console.html
var stateConsoleTemplate string

// stateConsoleCatalogs holds one flat message catalog per State Console locale.
// English is the fallback; the client resolves the locale in the browser.
//
//go:embed assets/state-console-i18n/*.json
var stateConsoleCatalogs embed.FS

const stateConsoleI18NPlaceholder = "{{STATE_CONSOLE_I18N}}"

var stateConsoleLocales = []string{"en", "de", "es", "zh-Hans", "hi", "ar"}

// stateConsoleHTML is the State Console page with every locale catalog inlined
// so the MCP App stays a single self-contained document.
var stateConsoleHTML = mustBuildStateConsoleHTML()

func mustBuildStateConsoleHTML() string {
	catalogs := make(map[string]map[string]string, len(stateConsoleLocales))
	for _, locale := range stateConsoleLocales {
		raw, err := stateConsoleCatalogs.ReadFile("assets/state-console-i18n/" + locale + ".json")
		if err != nil {
			panic(fmt.Sprintf("state console catalog %s: %v", locale, err))
		}
		messages := map[string]string{}
		if err := json.Unmarshal(raw, &messages); err != nil {
			panic(fmt.Sprintf("state console catalog %s: %v", locale, err))
		}
		catalogs[locale] = messages
	}
	// json.Marshal escapes <, > and & so the payload cannot close the script tag.
	payload, err := json.Marshal(catalogs)
	if err != nil {
		panic(fmt.Sprintf("state console catalogs: %v", err))
	}
	return strings.Replace(stateConsoleTemplate, stateConsoleI18NPlaceholder, string(payload), 1)
}
