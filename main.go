package main

import (
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var pluginVersion = "0.2.2"

const pluginName = "joycode"

func buildPlugin() pluginapi.Plugin {
	return pluginapi.Plugin{
		Metadata: pluginapi.Metadata{
			Name:             pluginName,
			Version:          pluginVersion,
			Author:           "Jadeiin",
			GitHubRepository: "https://github.com/Jadeiin/cpa-plugin-joycode",
		},
		Capabilities: pluginapi.Capabilities{
			ExecutorModelScope:    pluginapi.ExecutorModelScopeBoth,
			ExecutorInputFormats:  []string{"chat-completions"},
			ExecutorOutputFormats: []string{"chat-completions"},
		},
	}
}
