// Copyright 2019 Gitea. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package task

import (
	"fmt"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/migrations/base"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/structs"
)

// taskQueue is a global queue of tasks
var taskQueue Queue

// Run a task
func Run(t *models.Task) error {
	switch t.Type {
	case structs.TaskTypeMigrateRepo:
		return runMigrateTask(t)
	default:
		return fmt.Errorf("Unknow task type: %d", t.Type)
	}
}

// Init will start the service to get all unfinished tasks and run them
func Init() error {
	switch setting.Task.QueueType {
	case setting.ChannelQueueType:
		taskQueue = NewChannelQueue(setting.Task.QueueLength)
	case setting.RedisQueueType:
		var err error
		addrs, pass, idx, err := parseConnStr(setting.Task.QueueConnStr)
		if err != nil {
			return err
		}
		taskQueue, err = NewRedisQueue(addrs, pass, idx)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("Unsupported task queue type: %v", setting.Task.QueueType)
	}

	go func() {
		if err := taskQueue.Run(); err != nil {
			log.Error("taskQueue.Run end failed: %v", err)
		}
	}()

	return nil
}

// MigrateRepository add migration repository to task
func MigrateRepository(doer, u *models.User, opts base.MigrateOptions) error {
	task, err := models.CreateMigrateTask(doer, u, opts)
	if err != nil {
		return err
	}

	return taskQueue.Push(task)
}
