package backupcustody

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupexec"
)

// PutS3TargetObject stores one already encrypted object below the bound
// target's prefix with a single SigV4-signed PUT, for artifacts that must be
// readable without the Kopia repository: the identity recovery set needs only
// the Owner's recovery key, never the repository passphrase held on the lost
// host. Kopia blob IDs are lower-case, so a name that starts with an
// upper-case letter is never listed or collected by Kopia maintenance. It
// returns the s3://bucket/key location.
func PutS3TargetObject(ctx context.Context, workspace string, authority S3TargetAuthority, name string, body []byte) (string, error) {
	if name == "" || name[0] < 'A' || name[0] > 'Z' || strings.Contains(name, "..") || strings.ContainsAny(name, "\\\r\n\x00") {
		return "", errors.New("backupcustody: invalid object name")
	}
	material, err := LoadS3Target(workspace, authority)
	defer Clear(material.Passphrase)
	if err != nil {
		return "", err
	}
	repository, err := backupexec.CanonicalS3Repository(material.repository(), material.Passphrase)
	if err != nil {
		return "", err
	}
	key := material.Prefix + name
	request, err := signedS3Put(ctx, repository, key, body, time.Now().UTC())
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("backupcustody: the backup target did not accept the object")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("backupcustody: the backup target refused the object (HTTP %d)", response.StatusCode)
	}
	return "s3://" + repository.Bucket + "/" + key, nil
}

// signedS3Put builds an AWS Signature Version 4 PUT. Amazon endpoints use
// virtual-hosted addressing (path style for bucket names with dots, which a
// wildcard certificate cannot cover), every other S3-compatible endpoint path
// style, as Kopia's client does by default.
func signedS3Put(ctx context.Context, repository backupexec.S3Repository, key string, body []byte, now time.Time) (*http.Request, error) {
	host, objectPath := repository.Endpoint, "/"+repository.Bucket+"/"+s3EscapePath(key)
	if hostname := strings.Split(repository.Endpoint, ":")[0]; strings.HasSuffix(hostname, ".amazonaws.com") && !strings.Contains(repository.Bucket, ".") {
		host, objectPath = repository.Bucket+"."+repository.Endpoint, "/"+s3EscapePath(key)
	}
	region := repository.Region
	if region == "" {
		region = "us-east-1"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://"+host+objectPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.URL.RawPath = objectPath
	request.ContentLength = int64(len(body))
	payload := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(payload[:])
	stamp, day := now.Format("20060102T150405Z"), now.Format("20060102")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	request.Header.Set("X-Amz-Date", stamp)
	signedHeaders := "content-type;host;x-amz-content-sha256;x-amz-date"
	canonical := strings.Join([]string{
		http.MethodPut, objectPath, "",
		"content-type:application/octet-stream\nhost:" + host + "\nx-amz-content-sha256:" + payloadHash + "\nx-amz-date:" + stamp + "\n",
		signedHeaders, payloadHash,
	}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	canonicalHash := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(canonicalHash[:])
	signingKey := []byte("AWS4" + repository.SecretAccessKey)
	for _, part := range []string{day, region, "s3", "aws4_request"} {
		signingKey = s3HMAC(signingKey, part)
	}
	signature := hex.EncodeToString(s3HMAC(signingKey, toSign))
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+repository.AccessKeyID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	return request, nil
}

func s3HMAC(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

// s3EscapePath encodes a key as SigV4 requires for S3: every byte except the
// unreserved characters and the slash as %XX.
func s3EscapePath(key string) string {
	const hexDigits = "0123456789ABCDEF"
	var out strings.Builder
	for index := 0; index < len(key); index++ {
		c := key[index]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' || c == '/' {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hexDigits[c>>4])
		out.WriteByte(hexDigits[c&0x0f])
	}
	return out.String()
}
