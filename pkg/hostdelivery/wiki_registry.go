package hostdelivery

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
)

const WikiRegistryRepository = "kombiverselabs/kombify-knowledge-node"
const WikiRegistrySecretRef = "secret://stackkits/wiki/registry-auth"
const PaperworkRegistryRepository = "kombiverselabs/paperwork-selfhost"
const PaperworkRegistrySecretRef = "secret://stackkits/paperwork/registry-auth"

var wikiRegistryTokenPattern = regexp.MustCompile(`^kpr1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{43}$`)

type WikiRegistryGrant struct {
	Repository    string    `json:"repository"`
	RegistryToken string    `json:"registryToken"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

func ParseWikiRegistryGrant(material []byte, now time.Time, minimumLifetime time.Duration) (WikiRegistryGrant, error) {
	return parseRegistryGrant(material, now, minimumLifetime, WikiRegistryRepository)
}

func ParsePaperworkRegistryGrant(material []byte, now time.Time, minimumLifetime time.Duration) (WikiRegistryGrant, error) {
	return parseRegistryGrant(material, now, minimumLifetime, PaperworkRegistryRepository)
}

// RegistryAudience returns the registry host the grant was minted for. The
// claim is not a local authorization decision: it binds the destination of a
// bearer that only the Gateway can validate, so a caller-supplied image cannot
// redirect the bearer to another host.
func (grant WikiRegistryGrant) RegistryAudience() (string, bool) {
	parts := strings.Split(grant.RegistryToken, ".")
	if len(parts) != 3 || parts[0] != "kpr1" {
		return "", false
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	var payload struct {
		Audience string `json:"audience"`
	}
	if json.Unmarshal(claims, &payload) != nil || payload.Audience == "" {
		return "", false
	}
	return payload.Audience, true
}

// AuthorizesRegistry reports whether the signed grant was minted for host.
func (grant WikiRegistryGrant) AuthorizesRegistry(host string) bool {
	audience, ok := grant.RegistryAudience()
	return ok && audience == host
}

func parseRegistryGrant(material []byte, now time.Time, minimumLifetime time.Duration, repository string) (WikiRegistryGrant, error) {
	var grant WikiRegistryGrant
	decoder := json.NewDecoder(bytes.NewReader(material))
	decoder.DisallowUnknownFields()
	if len(material) > 2048 || decoder.Decode(&grant) != nil || decoder.Decode(new(any)) != io.EOF ||
		grant.Repository != repository || len(grant.RegistryToken) > 1024 ||
		!wikiRegistryTokenPattern.MatchString(grant.RegistryToken) || !grant.ExpiresAt.After(now.Add(minimumLifetime)) ||
		grant.ExpiresAt.After(now.Add(11*time.Minute)) {
		return WikiRegistryGrant{}, errors.New("registry pull grant is malformed, expired or too close to expiry")
	}
	return grant, nil
}
