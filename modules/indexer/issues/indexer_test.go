// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package issues

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/setting"

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	models.MainTest(m, filepath.Join("..", "..", ".."))
}

func TestBleveSearchIssues(t *testing.T) {
	assert.NoError(t, models.PrepareTestDatabase())

	tmpIndexerDir, err := ioutil.TempDir("", "issues-indexer")
	if err != nil {
		assert.Fail(t, "Unable to create temporary directory: %v", err)
		return
	}
	oldQueueDir := setting.Indexer.IssueQueueDir
	oldIssuePath := setting.Indexer.IssuePath
	setting.Indexer.IssueQueueDir = path.Join(tmpIndexerDir, "issues.queue")
	setting.Indexer.IssuePath = path.Join(tmpIndexerDir, "issues.queue")
	defer func() {
		setting.Indexer.IssueQueueDir = oldQueueDir
		setting.Indexer.IssuePath = oldIssuePath
		os.RemoveAll(tmpIndexerDir)
	}()

	setting.Indexer.IssueType = "bleve"
	InitIssueIndexer(true)
	defer func() {
		indexer := holder.get()
		if bleveIndexer, ok := indexer.(*BleveIndexer); ok {
			bleveIndexer.Close()
		}
	}()

	time.Sleep(5 * time.Second)

	ids, err := SearchIssuesByKeyword([]int64{1}, "issue2")
	assert.NoError(t, err)
	assert.EqualValues(t, []int64{2}, ids)

	ids, err = SearchIssuesByKeyword([]int64{1}, "first")
	assert.NoError(t, err)
	assert.EqualValues(t, []int64{1}, ids)

	ids, err = SearchIssuesByKeyword([]int64{1}, "for")
	assert.NoError(t, err)
	assert.EqualValues(t, []int64{1, 2, 3, 5}, ids)

	ids, err = SearchIssuesByKeyword([]int64{1}, "good")
	assert.NoError(t, err)
	assert.EqualValues(t, []int64{1}, ids)

}

func TestDBSearchIssues(t *testing.T) {
	assert.NoError(t, models.PrepareTestDatabase())

	setting.Indexer.IssueType = "db"
	InitIssueIndexer(true)

	ids, err := SearchIssuesByKeyword([]int64{1}, "issue2")
	assert.NoError(t, err)
	assert.EqualValues(t, []int64{2}, ids)

	ids, err = SearchIssuesByKeyword([]int64{1}, "first")
	assert.NoError(t, err)
	assert.EqualValues(t, []int64{1}, ids)

	ids, err = SearchIssuesByKeyword([]int64{1}, "for")
	assert.NoError(t, err)
	assert.EqualValues(t, []int64{1, 2, 3, 5}, ids)

	ids, err = SearchIssuesByKeyword([]int64{1}, "good")
	assert.NoError(t, err)
	assert.EqualValues(t, []int64{1}, ids)
}
