package assets

import "embed"

//
//go:embed email/templates/*
//go:embed static_assets/*
//go:embed frontend_src/dist/*
var Assets embed.FS
