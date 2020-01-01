// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package ssh

import (
	"code.gitea.io/gitea/modules/graceful"
	"code.gitea.io/gitea/modules/log"

	"github.com/gliderlabs/ssh"
)

func listen(server *ssh.Server) {
	gracefulServer := graceful.NewServer("tcp", server.Addr)

	err := gracefulServer.ListenAndServe(server.Serve)
	if err != nil {
		log.Critical("Failed to start SSH server: %v", err)
	}
	log.Info("SSH Listener: %s Closed", server.Addr)

}

// Unused informs our cleanup routine that we will not be using a ssh port
func Unused() {
	graceful.GetManager().InformCleanup()
}
