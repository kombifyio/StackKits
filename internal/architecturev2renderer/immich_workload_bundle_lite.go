package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	immichLiteWorkloadModuleID    = "stackkits-immich-lite-runtime"
	immichLiteWorkloadTemplateRef = "builtin://workloads/immich-lite/bundle/v2.json"
	immichLiteWorkloadOutputRef   = "workloads/immich-lite/bundle.json"
)

const immichLiteWorkloadRendererSchema = `stackkit.workload-bundle/v2|ImmichWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:server,postgres,postgres-init,valkey|secret-material:not-included|machine-learning:omitted`

func ImmichLiteWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(immichLiteWorkloadRendererSchema))
	return RendererContract{
		Kind: "native-config", RendererRef: immichWorkloadRendererRef,
		TemplateRef: immichLiteWorkloadTemplateRef, Version: immichWorkloadVersion,
		ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type immichLiteWorkloadBundleRenderer struct{ contract RendererContract }

func newImmichLiteWorkloadBundleRenderer() immichLiteWorkloadBundleRenderer {
	return immichLiteWorkloadBundleRenderer{contract: ImmichLiteWorkloadBundleRendererContract()}
}

func (r immichLiteWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateImmichWorkloadUnit(unit, r.contract, immichLiteWorkloadModuleID, true)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.immich-lite-workload", "marshal governed workload bundle", err)
	}
	data = append(data, '\n')
	return []UnitOutput{{Ref: immichLiteWorkloadOutputRef, Bytes: data}}, nil
}

var _ UnitRenderer = immichLiteWorkloadBundleRenderer{}
