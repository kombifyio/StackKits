package commands

import "os"

func shortHash(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func trimTrailingSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}
	return value
}

// firstEnv returns the first non-empty value among the given environment
// variables. It moved here when the platform-app deployment path it used to
// live beside was removed with the retired OpenTofu Apply body.
func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}
