package certtypes

type Certificate struct {
	ServiceID         string   `json:"serviceId"`
	Hostname          string   `json:"hostname"`
	Port              int      `json:"port"`
	Health            string   `json:"health"`
	CertVaultStatus   *string  `json:"certVaultStatus,omitempty"`
	Trusted           bool     `json:"trusted"`
	HostnameValid     bool     `json:"hostnameValid"`
	ValidFrom         *string  `json:"validFrom"`
	ValidTo           *string  `json:"validTo"`
	DaysRemaining     *int     `json:"daysRemaining"`
	Issuer            *string  `json:"issuer"`
	Serial            *string  `json:"serial"`
	FingerprintSHA256 *string  `json:"fingerprintSha256"`
	Domains           []string `json:"domains"`
	Error             string   `json:"error,omitempty"`
}
