package profiles

type Profile struct {
	PubKey      string `json:"pubkey"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
	Website     string `json:"website,omitempty"`
	Contact     string `json:"contact,omitempty"`
	Timestamp   int64  `json:"timestamp"`
}

type SignedProfile struct {
	Profile   Profile `json:"profile"`
	Signature string  `json:"signature"`
}
