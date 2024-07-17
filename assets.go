package assets

import "embed"

//go:embed email/templates/*
//go:embed static_assets/*
var Assets embed.FS
