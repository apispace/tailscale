// Copyright (c) 2020 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux darwin freebsd openbsd

package router

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"inet.af/netaddr"
	"tailscale.com/atomicfile"
)

const (
	tsConf     = "/etc/resolv.tailscale.conf"
	backupConf = "/etc/resolv.pre-tailscale-backup.conf"
	resolvConf = "/etc/resolv.conf"
)

// writeTsConf writes DNS configuration in resolv.conf format to the given writer.
func writeTsConf(w io.Writer, servers []netaddr.IP, domains []string) {
	io.WriteString(w, "# resolv.conf(5) file generated by tailscale\n")
	io.WriteString(w, "# DO NOT EDIT THIS FILE BY HAND -- CHANGES WILL BE OVERWRITTEN\n\n")
	for _, ns := range servers {
		io.WriteString(w, "nameserver ")
		io.WriteString(w, ns.String())
		io.WriteString(w, "\n")
	}
	if len(domains) > 0 {
		io.WriteString(w, "search")
		for _, domain := range domains {
			io.WriteString(w, " ")
			io.WriteString(w, domain)
		}
		io.WriteString(w, "\n")
	}
}

// dnsDirectUp replaces /etc/resolv.conf with a file generated
// from the given configuration, creating a backup of its old state.
//
// This way of configuring DNS is precarious, since it does not react
// to the disappearance of the Tailscale interface.
// The caller must call dnsDirectDown before program shutdown
// and ensure that router.Cleanup is run if the program terminates unexpectedly.
func dnsDirectUp(config DNSConfig) error {
	tf, err := ioutil.TempFile(filepath.Dir(tsConf), filepath.Base(tsConf)+".*")
	if err != nil {
		return err
	}
	tempName := tf.Name()
	tf.Close()

	// Write the tsConf file.
	buf := new(bytes.Buffer)
	writeTsConf(buf, config.Nameservers, config.Domains)
	if err := atomicfile.WriteFile(tempName, buf.Bytes(), 0644); err != nil {
		return err
	}
	if err := os.Rename(tempName, tsConf); err != nil {
		return err
	}

	if linkPath, err := os.Readlink(resolvConf); err != nil {
		// Remove any old backup that may exist.
		os.Remove(backupConf)

		// Backup the existing /etc/resolv.conf file.
		contents, err := ioutil.ReadFile(resolvConf)
		if os.IsNotExist(err) {
			// No existing /etc/resolv.conf file to backup.
			// Nothing to do.
			return nil
		} else if err != nil {
			return err
		}
		if err := atomicfile.WriteFile(backupConf, contents, 0644); err != nil {
			return err
		}
	} else if linkPath != tsConf {
		// Backup the existing symlink.
		os.Remove(backupConf)
		if err := os.Symlink(linkPath, backupConf); err != nil {
			return err
		}
	} else {
		// Nothing to do, resolvConf already points to tsConf.
		return nil
	}

	os.Remove(resolvConf)
	if err := os.Symlink(tsConf, resolvConf); err != nil {
		return err
	}

	return nil
}

// dnsDirectDown restores /etc/resolv.conf to its state before replaceResolvConf.
// It is idempotent and behaves correctly even if replaceResolvConf has never been run.
func dnsDirectDown() error {
	if _, err := os.Stat(backupConf); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if ln, err := os.Readlink(resolvConf); err != nil {
		return err
	} else if ln != tsConf {
		return fmt.Errorf("resolv.conf is not a symlink to %s", tsConf)
	}
	if err := os.Rename(backupConf, resolvConf); err != nil {
		return err
	}
	os.Remove(tsConf)
	return nil
}