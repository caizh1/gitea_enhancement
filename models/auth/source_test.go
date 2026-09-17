// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth_test

import (
	"errors"
	"strings"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm/schemas"
)

type TestSource struct {
	auth_model.ConfigBase `json:"-"`

	TestField string
	prepared  chan<- struct{} `json:"-"`
	continueC <-chan struct{} `json:"-"`
}

type testPreparedSourceChange struct{}

func (testPreparedSourceChange) Commit(commit func() error) error { return commit() }

func (source *TestSource) PrepareSourceChange(_, _ *auth_model.Source) (auth_model.PreparedSourceChange, error) {
	if source.prepared != nil {
		source.prepared <- struct{}{}
		<-source.continueC
	}
	return testPreparedSourceChange{}, nil
}

// FromDB fills up a LDAPConfig from serialized format.
func (source *TestSource) FromDB(bs []byte) error {
	return json.Unmarshal(bs, &source)
}

// ToDB exports a LDAPConfig to a serialized format.
func (source *TestSource) ToDB() ([]byte, error) {
	return json.Marshal(source)
}

func TestDumpAuthSource(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	authSourceSchema, err := unittest.GetXORMEngine().TableInfo(new(auth_model.Source))
	require.NoError(t, err)

	auth_model.RegisterTypeConfig(auth_model.OAuth2, new(TestSource))
	source := &auth_model.Source{
		Type: auth_model.OAuth2,
		Name: "TestSource",
		Cfg:  &TestSource{TestField: "TestValue"},
	}
	require.NoError(t, auth_model.CreateSource(t.Context(), source))

	// intentionally test the "dump" to make sure the dumped JSON is correct: https://github.com/go-gitea/gitea/pull/16847
	sb := &strings.Builder{}
	require.NoError(t, unittest.GetXORMEngine().DumpTables([]*schemas.Table{authSourceSchema}, sb))
	// the dumped SQL is something like:
	// INSERT INTO `login_source` (`id`, `type`, `name`, `is_active`, `is_sync_enabled`, `two_factor_policy`, `cfg`, `created_unix`, `updated_unix`) VALUES (1,6,'TestSource',0,0,'','{"TestField":"TestValue"}',1774179784,1774179784);
	assert.Contains(t, sb.String(), `'{"TestField":"TestValue"}'`)
}

func TestSourceAuditRollbackAndConcurrentUpdate(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	auth_model.RegisterTypeConfig(auth_model.OAuth2, new(TestSource))
	source := &auth_model.Source{Type: auth_model.OAuth2, Name: "事务认证源", IsActive: true, Cfg: &TestSource{TestField: "初始"}}
	require.NoError(t, auth_model.CreateSource(t.Context(), source))
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "authentication.source_created", ObjectID: source.ID})

	first, err := auth_model.GetSourceByID(t.Context(), source.ID)
	require.NoError(t, err)
	stale, err := auth_model.GetSourceByID(t.Context(), source.ID)
	require.NoError(t, err)
	stale.Name = "事务认证源二"
	prepared, continueC := make(chan struct{}, 1), make(chan struct{})
	stale.Cfg = &TestSource{TestField: "并发修改", prepared: prepared, continueC: continueC}
	result := make(chan error, 1)
	go func() { result <- auth_model.UpdateSource(t.Context(), stale) }()
	<-prepared
	first.Name = "事务认证源一"
	require.NoError(t, auth_model.UpdateSource(t.Context(), first))
	close(continueC)
	assert.ErrorIs(t, <-result, governance_model.ErrConflict)

	_, err = db.GetEngine(t.Context()).ID(1).Delete(new(governance_model.WriteLock))
	require.NoError(t, err)
	failed := &auth_model.Source{Type: auth_model.OAuth2, Name: "不得提交", IsActive: true, Cfg: &TestSource{}}
	err = auth_model.CreateSource(t.Context(), failed)
	require.True(t, errors.Is(err, governance_model.ErrConflict))
	assert.Zero(t, failed.ID)
	unittest.AssertNotExistsBean(t, &auth_model.Source{Name: "不得提交"})
}
