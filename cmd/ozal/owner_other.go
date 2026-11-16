//go:build !unix

package main

import "os"

func sameOwner(string, os.FileInfo) error { return nil }

func sameOwnerInRoot(*os.Root, string, os.FileInfo) error { return nil }
