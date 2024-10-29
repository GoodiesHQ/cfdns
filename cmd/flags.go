package main

import "flag"

const DEFAULT_CONFIG_FILE = "cfdns.yaml"

type CLIFlags struct {
	ConfigFile string
}

func cli() *CLIFlags {
	var cliFlags CLIFlags
	flag.StringVar(&cliFlags.ConfigFile, "config", "", "Configuration File")
	flag.StringVar(&cliFlags.ConfigFile, "c", "", "Configuration File (alias)")
	flag.Parse()

	if cliFlags.ConfigFile == "" {
		cliFlags.ConfigFile = DEFAULT_CONFIG_FILE
	}

	return &cliFlags
}
