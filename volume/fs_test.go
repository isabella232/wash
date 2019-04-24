package volume

import (
	"context"
	"testing"

	"github.com/puppetlabs/wash/datastore"
	"github.com/puppetlabs/wash/plugin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

const varLogFixture = `
96 1550611510 1550611448 1550611448 41ed /var/log/path
96 1550611510 1550611448 1550611448 41ed /var/log/path/has
96 1550611510 1550611448 1550611448 41ed /var/log/path/has/got
96 1550611510 1550611458 1550611458 41ed /var/log/path/has/got/some
0 1550611458 1550611458 1550611458 81a4 /var/log/path/has/got/some/legs
96 1550611510 1550611453 1550611453 41ed /var/log/path1
0 1550611453 1550611453 1550611453 81a4 /var/log/path1/a file
96 1550611510 1550611441 1550611441 41ed /var/log/path2
64 1550611510 1550611441 1550611441 41ed /var/log/path2/dir
`

type fsTestSuite struct {
	suite.Suite
}

func (suite *fsTestSuite) SetupSuite() {
	plugin.SetTestCache(datastore.NewMemCache())
}

func (suite *fsTestSuite) TearDownSuite() {
	plugin.UnsetTestCache()
}

func createResult(data string) plugin.ExecResult {
	outputch := make(chan plugin.ExecOutputChunk, 1)
	execResult := plugin.ExecResult{
		OutputCh:   outputch,
		ExitCodeCB: func() (int, error) { return 0, nil },
	}
	outputch <- plugin.ExecOutputChunk{Data: data}
	close(outputch)
	return execResult
}

func createExec() *mockExecutor {
	exec := &mockExecutor{EntryBase: plugin.NewEntry("instance")}
	// Used when recording activity.
	exec.SetTestID("/instance")
	cmd := StatCmd("/var/log")
	exec.On("Exec", mock.Anything, cmd[0], cmd[1:], plugin.ExecOptions{}).Return(createResult(varLogFixture), nil)
	return exec
}

func (suite *fsTestSuite) TestFSList() {
	exec := createExec()
	fs := NewFS("fs", exec)
	// ID would normally be set when listing FS within the parent instance.
	fs.SetTestID("/instance/fs")

	entries, err := fs.List(context.Background())
	suite.NoError(err)
	suite.Equal(3, len(entries))

	suite.Equal("path", plugin.Name(entries[0]))
	suite.Equal("path1", plugin.Name(entries[1]))
	suite.Equal("path2", plugin.Name(entries[2]))
	for _, entry := range entries {
		_, ok := entry.(plugin.Group)
		suite.True(ok)
	}

	entries1, err := entries[1].(plugin.Group).List(context.Background())
	suite.NoError(err)
	suite.Equal(1, len(entries1))
	suite.Equal("a file", plugin.Name(entries1[0]))
	_, ok := entries1[0].(plugin.Readable)
	suite.True(ok)

	entries2, err := entries[2].(plugin.Group).List(context.Background())
	suite.NoError(err)
	suite.Equal(1, len(entries2))
	suite.Equal("dir", plugin.Name(entries2[0]))
	_, ok = entries2[0].(plugin.Group)
	suite.True(ok)
}

func (suite *fsTestSuite) TestFSRead() {
	exec := createExec()
	fs := NewFS("fs", exec)
	// ID would normally be set when listing FS within the parent instance.
	fs.SetTestID("/instance/fs")

	entries, err := fs.List(context.Background())
	entries1, err := entries[1].(plugin.Group).List(context.Background())
	suite.Equal("a file", plugin.Name(entries1[0]))

	execResult := createResult("hello")
	exec.On("Exec", mock.Anything, "cat", []string{"/var/log/path1/a file"}, plugin.ExecOptions{}).Return(execResult, nil)
	rdr, err := entries1[0].(plugin.Readable).Open(context.Background())
	suite.NoError(err)
	suite.Equal(int64(5), rdr.Size())
}

func TestCache(t *testing.T) {
	suite.Run(t, new(fsTestSuite))
}

type mockExecutor struct {
	plugin.EntryBase
	mock.Mock
}

func (m *mockExecutor) Exec(ctx context.Context, cmd string, args []string, opts plugin.ExecOptions) (plugin.ExecResult, error) {
	arger := m.Called(ctx, cmd, args, opts)
	return arger.Get(0).(plugin.ExecResult), arger.Error(1)
}
