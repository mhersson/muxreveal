//go:build !darwin

package main

// maxSockPath is the longest Unix socket path bind accepts (sun_path is 108
// bytes including the NUL on Linux).
const maxSockPath = 107
