package hostdelivery

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"
)

const WikiRegistryRepository = "kombiverselabs/kombify-knowledge-node"
const WikiRegistrySecretRef = "secret://stackkits/wiki/registry-auth"

var wikiRegistryTokenPattern = regexp.MustCompile(`^kpr1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{43}$`)

type WikiRegistryGrant struct {
	Repository    string    `json:"repository"`
	RegistryToken string    `json:"registryToken"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

func ParseWikiRegistryGrant(material []byte, now time.Time, minimumLifetime time.Duration) (WikiRegistryGrant, error) {
	var grant WikiRegistryGrant
	decoder := json.NewDecoder(bytes.NewReader(material))
	decoder.DisallowUnknownFields()
	if len(material) > 2048 || decoder.Decode(&grant) != nil || decoder.Decode(new(any)) != io.EOF ||
		grant.Repository != WikiRegistryRepository || len(grant.RegistryToken) > 1024 ||
		!wikiRegistryTokenPattern.MatchString(grant.RegistryToken) || !grant.ExpiresAt.After(now.Add(minimumLifetime)) ||
		grant.ExpiresAt.After(now.Add(11*time.Minute)) {
		return WikiRegistryGrant{}, errors.New("wiki registry pull grant is malformed, expired or too close to expiry")
	}
	return grant, nil
}
