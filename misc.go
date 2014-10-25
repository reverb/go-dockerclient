// Copyright 2014 go-dockerclient authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/docker/docker/pkg/promise"
)

// Version returns version information about the docker server.
//
// See http://goo.gl/BOZrF5 for more details.
func (c *Client) Version() (*Env, error) {
	body, _, err := c.do("GET", "/version", nil)
	if err != nil {
		return nil, err
	}
	var env Env
	if err := env.Decode(bytes.NewReader(body)); err != nil {
		return nil, err
	}
	return &env, nil
}

// Info returns system-wide information about the Docker server.
//
// See http://goo.gl/wmqZsW for more details.
func (c *Client) Info() (*Env, error) {
	body, _, err := c.do("GET", "/info", nil)
	if err != nil {
		return nil, err
	}
	var info Env
	err = info.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return &info, nil
}

// ExecOptions present the set of options available for pulling an image
// from a registry.
//
// See http://docs.docker.com/reference/api/docker_remote_api_v1.15/#exec-create for more details.
type ExecOptions struct {
	User         string
	Privileged   bool
	AttachStdin  bool
	AttachStdout bool
	AttachStderr bool
	Tty          bool
	Command      []string `json:"Cmd"`
	Container    string
	OutputStream io.Writer `json:"-"`
	ErrorStream  io.Writer `json:"-"`
	InputStream  io.Reader `json:"-"`
}

func (c *Client) Exec(opts ExecOptions) error {
	if opts.Container == "" {
		return &NoSuchContainer{ID: opts.Container}
	}
	name := opts.Container
	path := "/containers/" + name + "/exec"
	body, _, err := c.do("POST", path, opts)
	if err != nil {
		return err
	}

	id := struct{ Id string }{}
	err = json.Unmarshal(body, &id)
	if err != nil {
		return err
	}
	if id.Id == "" {
		return fmt.Errorf("Couldn't get an operation id for the exec command")
	}
	var (
		hijacked = make(chan io.Closer)
		errCh    chan error
	)
	// Block the return until the chan gets closed
	defer func() {
		if _, ok := <-hijacked; ok {
			fmt.Println("Hijack did not finish (chan still open)")
		}
	}()

	doPath := "/exec/" + id.Id + "/start"
	errCh = promise.Go(func() error {
		stderr := opts.ErrorStream
		if opts.Tty {
			stderr = opts.OutputStream
		}
		return c.hijack2("POST", doPath, opts.Tty, opts.InputStream, opts.OutputStream, stderr, hijacked, opts)
	})

	select {
	case closer := <-hijacked:
		if closer != nil {
			defer closer.Close()
		}
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	var (
		isTerminalIn, isTerminalOut bool
		outFd                       uintptr
	)

	if _, ok := opts.InputStream.(*os.File); ok {
		isTerminalIn = true
	}
	if file, ok := opts.OutputStream.(*os.File); ok {
		isTerminalOut = true
		outFd = file.Fd()
	}

	if opts.Tty && isTerminalIn {
		if err := c.monitorTtySize(id.Id, true, isTerminalOut, outFd); err != nil {
			fmt.Printf("Error monitoring TTY size: %s\n", err)
		}
	}

	if err := <-errCh; err != nil {
		return err
	}
	return nil
}

// ParseRepositoryTag gets the name of the repository and returns it splitted
// in two parts: the repository and the tag.
//
// Some examples:
//
//     localhost.localdomain:5000/samalba/hipache:latest -> localhost.localdomain:5000/samalba/hipache, latest
//     localhost.localdomain:5000/samalba/hipache -> localhost.localdomain:5000/samalba/hipache, ""
func ParseRepositoryTag(repoTag string) (repository string, tag string) {
	n := strings.LastIndex(repoTag, ":")
	if n < 0 {
		return repoTag, ""
	}
	if tag := repoTag[n+1:]; !strings.Contains(tag, "/") {
		return repoTag[:n], tag
	}
	return repoTag, ""
}
