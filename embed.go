package stackkits

import (
	"bytes"
	"embed"
)

//go:embed cue.mod/module.cue foundation/*.cue
var productCUESources embed.FS

//go:embed architecture/v2/contractfixture/catalog.cue basement-kit/stackfile.cue
var contractFixtureCUESources embed.FS

func ReadAuthorityCUESource(path string, fixture bool) ([]byte, error) {
	data, err := productCUESources.ReadFile(path)
	if err != nil && fixture {
		data, err = contractFixtureCUESources.ReadFile(path)
	}
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), err
}
