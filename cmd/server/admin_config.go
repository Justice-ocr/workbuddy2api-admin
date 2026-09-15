package main

import (
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
)

func (c *Config) validateAdmin() error {
	if c.AdminTokenFile != "" {
		if c.AdminToken != "" {
			return errors.New("set only one admin token source")
		}
		st, err := os.Lstat(c.AdminTokenFile)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 4096 {
			return errors.New("invalid admin token file")
		}
		if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
			return errors.New("admin token file must be owner-only (0600)")
		}
		raw, err := os.ReadFile(c.AdminTokenFile)
		if err != nil {
			return errors.New("cannot read admin token file")
		}
		c.AdminToken = strings.TrimSpace(string(raw))
	}
	if c.AdminToken == "" {
		return nil
	}
	if len(c.AdminToken) < 32 || len(c.AdminToken) > 256 || strings.ContainsAny(c.AdminToken, " \r\n\t") {
		return errors.New("admin token must contain 32 to 256 non-whitespace bytes")
	}
	if c.AdminToken == c.APIKey {
		return errors.New("admin token must differ from API key")
	}
	if !loopbackHost(c.AdminListen) {
		host, port, err := net.SplitHostPort(c.AdminListen)
		if !c.AdminAllowContainerBind || err != nil || host != "0.0.0.0" || !loopbackHost(net.JoinHostPort("127.0.0.1", port)) {
			return errors.New("admin listener must use loopback; container bind requires explicit opt-in")
		}
	}
	return nil
}
