// Copyright 2017 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package private

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"code.gitea.io/gitea/modules/httplib"
	"code.gitea.io/gitea/modules/setting"
)

func newRequest(url, method string) *httplib.Request {
	return httplib.NewRequest(url, method).Header("Authorization",
		fmt.Sprintf("Bearer %s", setting.InternalToken))
}

// Response internal request response
type Response struct {
	Err string `json:"err"`
}

func decodeJSONError(resp *http.Response) *Response {
	var res Response
	err := json.NewDecoder(resp.Body).Decode(&res)
	if err != nil {
		res.Err = err.Error()
	}
	return &res
}

func newInternalRequest(url, method string) *httplib.Request {
	req := newRequest(url, method).SetTLSClientConfig(&tls.Config{
		InsecureSkipVerify: true,
		ServerName:         setting.Domain,
	})
	if setting.Protocol == setting.UnixSocket {
		req.SetTransport(&http.Transport{
			Dial: func(_, _ string) (net.Conn, error) {
				return net.Dial("unix", setting.HTTPAddr)
			},
		})
	}
	return req
}
