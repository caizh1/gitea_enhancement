// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2

import (
	"gitea.dev/models/auth"
	"gitea.dev/modules/json"
	"net/url"
	"slices"
)

func auditURLOrigin(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Hostname()
}

func (source *Source) AuditSourceConfig() map[string]any {
	scopes := slices.Clone(source.Scopes)
	slices.Sort(scopes)
	return map[string]any{
		"provider": source.Provider, "client_id": source.ClientID, "client_secret_configured": source.ClientSecret != "",
		"discovery_origin": auditURLOrigin(source.OpenIDConnectAutoDiscoveryURL), "scopes": scopes,
		"required_claim_name": source.RequiredClaimName, "group_claim_name": source.GroupClaimName,
		"ssh_public_key_claim_name": source.SSHPublicKeyClaimName, "external_id_claim": source.ExternalIDClaim,
	}
}

// Source holds configuration for the OAuth2 login source.
type Source struct {
	auth.ConfigBase `json:"-"`

	Provider                      string
	ClientID                      string
	ClientSecret                  string
	OpenIDConnectAutoDiscoveryURL string
	CustomURLMapping              *CustomURLMapping
	IconURL                       string

	Scopes              []string
	RequiredClaimName   string
	RequiredClaimValue  string
	GroupClaimName      string
	AdminGroup          string
	GroupTeamMap        string
	GroupTeamMapRemoval bool
	RestrictedGroup     string

	SSHPublicKeyClaimName string
	FullNameClaimName     string
	ExternalIDClaim       string
}

// FromDB fills up an OAuth2Config from serialized format.
func (source *Source) FromDB(bs []byte) error {
	return json.UnmarshalHandleDoubleEncode(bs, &source)
}

// ToDB exports an OAuth2Config to a serialized format.
func (source *Source) ToDB() ([]byte, error) {
	return json.Marshal(source)
}

func init() {
	auth.RegisterTypeConfig(auth.OAuth2, &Source{})
}
