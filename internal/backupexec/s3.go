package backupexec

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// ConnectS3Repository connects only to an existing repository. A different
// configured target is rejected without disconnecting, creating or adopting it.
// Kopia owns its repository configuration; this method never creates a bucket.
func (e V2Engine) ConnectS3Repository(ctx context.Context, repo S3Repository, password []byte) (RepositoryStatus, error) {
	endpoint, input, err := prepareS3Repository(repo, password)
	if err != nil {
		return RepositoryStatus{}, err
	}
	defer clear(input)
	readStatus := func() (RepositoryStatus, error) {
		status, err := e.RepositoryStatus(ctx, password)
		if err != nil {
			return RepositoryStatus{}, &safeDiagnosticError{message: redactSensitiveValue(err.Error(), input)}
		}
		return status, nil
	}
	exact := func(status RepositoryStatus) bool {
		return status.Configured && status.ConfigFile == e.configFile() && status.Storage == "s3" && !status.InsecureTLS &&
			status.S3Endpoint == endpoint && status.S3Bucket == repo.Bucket && status.S3Prefix == repo.Prefix && status.S3Region == repo.Region
	}
	status, err := readStatus()
	if err != nil {
		return RepositoryStatus{}, err
	}
	if status.Configured {
		if !exact(status) {
			return RepositoryStatus{}, errors.New("configured Kopia repository differs from the bound S3 target")
		}
		return status, nil
	}
	args := []string{"repository", "connect", "s3", "--endpoint", endpoint, "--bucket", repo.Bucket, "--prefix", repo.Prefix, "--region", repo.Region}
	if e.offsite {
		args = append(args, "--cache-directory", DefaultCacheDirectory+"/offsite")
	}
	_, err = e.invoke(ctx, args, input)
	if err != nil {
		return RepositoryStatus{}, err
	}
	status, err = readStatus()
	if err != nil {
		return RepositoryStatus{}, err
	}
	if !exact(status) {
		return RepositoryStatus{}, errors.New("connected Kopia repository does not match the bound S3 target")
	}
	return status, nil
}

func canonicalS3Endpoint(endpoint string) (string, error) {
	if endpoint == "" || strings.TrimSpace(endpoint) != endpoint {
		return "", errors.New("S3 endpoint is required")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("S3 endpoint must be an HTTPS host without credentials or a path")
	}
	return parsed.Host, nil
}

// ValidateS3Repository validates owner-supplied material without runtime effects.
func ValidateS3Repository(repo S3Repository, password []byte) error {
	_, input, err := prepareS3Repository(repo, password)
	clear(input)
	return err
}

func prepareS3Repository(repo S3Repository, password []byte) (string, []byte, error) {
	endpoint, err := canonicalS3Endpoint(repo.Endpoint)
	if err != nil {
		return "", nil, err
	}
	if repo.Bucket == "" || strings.ContainsAny(repo.Bucket, "/\\ \t\r\n\x00") || strings.ContainsAny(repo.Prefix+repo.Region, "\r\n\x00") {
		return "", nil, errors.New("S3 repository target is invalid")
	}
	input, err := repositoryPasswordInput(password, 1)
	if err != nil {
		return "", nil, err
	}
	for _, credential := range []string{repo.AccessKeyID, repo.SecretAccessKey} {
		if strings.TrimSpace(credential) == "" || strings.ContainsAny(credential, "\r\n\x00") {
			clear(input)
			return "", nil, errors.New("S3 repository credentials must be nonempty single lines")
		}
		input = append(input, credential...)
		input = append(input, '\n')
	}
	return endpoint, input, nil
}

// CanonicalS3Repository returns the exact endpoint representation used by Kopia.
func CanonicalS3Repository(repo S3Repository, password []byte) (S3Repository, error) {
	endpoint, input, err := prepareS3Repository(repo, password)
	clear(input)
	if err != nil {
		return S3Repository{}, err
	}
	repo.Endpoint = endpoint
	return repo, nil
}
