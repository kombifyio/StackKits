package advancedcapability

import (
	"fmt"
	"strconv"
)

func canonicalUnsigned(envelope envelope) []byte {
	document := make([]byte, 0, 1024)
	document = append(document, '{')
	document = appendStringArrayProperty(document, "allowedOperations", envelope.AllowedOperations)
	document = append(document, ',')
	document = appendStringProperty(document, "audience", envelope.Audience)
	document = append(document, ',')
	document = appendStringProperty(document, "capabilityId", envelope.CapabilityID)
	document = append(document, ',')
	document = appendStringProperty(document, "expiresAt", envelope.ExpiresAt)
	document = append(document, ',')
	document = appendStringProperty(document, "issuedAt", envelope.IssuedAt)
	document = append(document, ',')
	document = appendStringProperty(document, "issuerId", envelope.IssuerID)
	document = append(document, ',')
	document = appendStringProperty(document, "keyId", envelope.KeyID)
	document = append(document, ',')
	document = appendStringProperty(document, "ownerRef", envelope.OwnerRef)
	document = append(document, ',')
	document = appendStringProperty(document, "rilRef", envelope.RILRef)
	document = append(document, ',')
	document = appendStringProperty(document, "schemaVersion", envelope.SchemaVersion)
	document = append(document, ',')
	document = appendStringProperty(document, "stackId", envelope.StackID)
	document = append(document, ',')
	document = appendStringProperty(document, "uiManagerRef", envelope.UIManagerRef)
	document = append(document, '}')
	return document
}

func canonicalDocument(envelope envelope) ([]byte, error) {
	unsigned := canonicalUnsigned(envelope)

	// RFC 8785 orders "signature" between "schemaVersion" and "stackId".
	marker := []byte(`,"stackId":`)
	index := indexBytes(unsigned, marker)
	if index < 0 {
		return nil, fmt.Errorf("canonical stackId marker is missing")
	}
	document := make([]byte, 0, len(unsigned)+len(envelope.Signature)+14)
	document = append(document, unsigned[:index]...)
	document = append(document, `,"signature":`...)
	document = strconv.AppendQuote(document, envelope.Signature)
	document = append(document, unsigned[index:]...)
	return document, nil
}

func appendStringProperty(document []byte, name, value string) []byte {
	document = strconv.AppendQuote(document, name)
	document = append(document, ':')
	return strconv.AppendQuote(document, value)
}

func appendStringArrayProperty(document []byte, name string, values []string) []byte {
	document = strconv.AppendQuote(document, name)
	document = append(document, ':', '[')
	for index, value := range values {
		if index > 0 {
			document = append(document, ',')
		}
		document = strconv.AppendQuote(document, value)
	}
	return append(document, ']')
}

func indexBytes(haystack, needle []byte) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		matched := true
		for offset := range needle {
			if haystack[index+offset] != needle[offset] {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return -1
}
