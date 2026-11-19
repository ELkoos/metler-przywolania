//go:build !windows

package main

func serviceUsage()                          {}
func serviceMain(args []string) (bool, error) { return false, nil }
