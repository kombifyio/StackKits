package commands

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
