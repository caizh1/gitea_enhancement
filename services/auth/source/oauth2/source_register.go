// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2

import (
	"fmt"

	"gitea.dev/models/auth"

	"github.com/markbates/goth"
)

type preparedSourceChange struct {
	provider goth.Provider
	oldName  string
	newName  string
}

func (change *preparedSourceChange) Commit(commit func() error) error {
	gothRWMutex.Lock()
	defer gothRWMutex.Unlock()
	if err := commit(); err != nil {
		return err
	}
	if change.oldName != "" {
		delete(goth.GetProviders(), change.oldName)
	}
	if change.provider != nil {
		goth.UseProviders(change.provider)
	}
	return nil
}

// PrepareSourceChange 在注册表锁外完成 OIDC 发现；Commit 中仅执行短事务和内存替换。
func (source *Source) PrepareSourceChange(previous, next *auth.Source) (auth.PreparedSourceChange, error) {
	change := &preparedSourceChange{}
	if previous != nil && previous.IsActive {
		change.oldName = previous.Name
	}
	if next == nil || !next.IsActive {
		return change, nil
	}
	provider, err := createProvider(next.Name, source)
	if err != nil {
		return nil, wrapOpenIDConnectInitializeError(err, next.Name, source)
	}
	change.newName, change.provider = next.Name, provider
	return change, nil
}

// RegisterSource causes an OAuth2 configuration to be registered
func (source *Source) RegisterSource() error {
	err := RegisterProviderWithGothic(source.AuthSource.Name, source)
	return wrapOpenIDConnectInitializeError(err, source.AuthSource.Name, source)
}

// UnregisterSource causes an OAuth2 configuration to be unregistered
func (source *Source) UnregisterSource() error {
	RemoveProviderFromGothic(source.AuthSource.Name)
	return nil
}

// ErrOpenIDConnectInitialize represents a "OpenIDConnectInitialize" kind of error.
type ErrOpenIDConnectInitialize struct {
	OpenIDConnectAutoDiscoveryURL string
	ProviderName                  string
	Cause                         error
}

// IsErrOpenIDConnectInitialize checks if an error is a ExternalLoginUserAlreadyExist.
func IsErrOpenIDConnectInitialize(err error) bool {
	_, ok := err.(ErrOpenIDConnectInitialize)
	return ok
}

func (err ErrOpenIDConnectInitialize) Error() string {
	return fmt.Sprintf("Failed to initialize OpenID Connect Provider with name '%s' with url '%s': %v", err.ProviderName, err.OpenIDConnectAutoDiscoveryURL, err.Cause)
}

func (err ErrOpenIDConnectInitialize) Unwrap() error {
	return err.Cause
}

// wrapOpenIDConnectInitializeError is used to wrap the error but this cannot be done in modules/auth/oauth2
// inside oauth2: import cycle not allowed models -> modules/auth/oauth2 -> models
func wrapOpenIDConnectInitializeError(err error, providerName string, source *Source) error {
	if err != nil && source.Provider == "openidConnect" {
		err = ErrOpenIDConnectInitialize{ProviderName: providerName, OpenIDConnectAutoDiscoveryURL: source.OpenIDConnectAutoDiscoveryURL, Cause: err}
	}
	return err
}
