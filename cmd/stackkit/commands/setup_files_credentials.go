package commands

// Files owner setup and household provisioning share this closed credential
// document. Reading it never changes or reissues the existing owner password.
type filesOwnerCredentials struct {
	Email                       string `json:"email"`
	Password                    string `json:"password"`
	Language                    string `json:"language"`
	AllowFirstOwnerRegistration bool   `json:"allowFirstOwnerRegistration"`
}

func readFilesOwnerCredentials(workspace, path string) (filesOwnerCredentials, error) {
	var credentials filesOwnerCredentials
	if err := readNativeSetupCredentialJSON(workspace, path, &credentials); err != nil {
		return filesOwnerCredentials{}, err
	}
	return credentials, nil
}
