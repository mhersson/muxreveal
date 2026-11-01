package main

// maxSockPath is the longest Unix socket path bind accepts (sun_path is 104
// bytes including the NUL).
const maxSockPath = 103
