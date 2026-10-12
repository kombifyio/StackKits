package main

import (
	"encoding/json"
	"strings"
	"syscall/js"

	"github.com/kombifyio/stackkits/internal/capacityfit"
)

func main() {
	assess := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return failure("capacity input must be one JSON document")
		}
		var input capacityfit.Input
		decoder := json.NewDecoder(strings.NewReader(args[0].String()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return failure("capacity input does not match the shared contract")
		}
		result, err := capacityfit.Assess(input)
		if err != nil {
			return failure(err.Error())
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return failure("capacity result could not be encoded")
		}
		return string(raw)
	})
	overall := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return failure("capacity verdicts must be one JSON array")
		}
		var verdicts []capacityfit.Verdict
		if err := json.Unmarshal([]byte(args[0].String()), &verdicts); err != nil {
			return failure("capacity verdicts do not match the shared contract")
		}
		for _, verdict := range verdicts {
			switch verdict {
			case capacityfit.Pass, capacityfit.Warning, capacityfit.Unverified, capacityfit.Fail:
			default:
				return failure("unknown capacity verdict")
			}
		}
		raw, _ := json.Marshal(map[string]any{"overall": capacityfit.Overall(verdicts...)})
		return string(raw)
	})
	js.Global().Set("__stackkitsCapacityFit", map[string]any{"assess": assess, "overall": overall})
	select {}
}

func failure(message string) string {
	raw, _ := json.Marshal(map[string]string{"error": message})
	return string(raw)
}
