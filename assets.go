package assets

import "embed"

//
//go:embed email/templates/*
//go:embed static_assets/*
//go:embed booksdump-frontend/build/*
var Assets embed.FS
