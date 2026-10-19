package image

import _ "embed"

//go:embed data/Dockerfile
var embeddedBaseToolsBlock []byte

//go:embed data/agents-sandbox-path.sh
var embeddedPathMergeScript []byte

//go:embed data/msb-trust.sh
var embeddedTrustHelperScript []byte

// pathMergeScript returns the profile.d merge script shipped in the build
// context and installed into the image.
func pathMergeScript() []byte { return embeddedPathMergeScript }

// trustHelperScript returns the CA-trust helper shipped in the build context and
// installed into the image. The bytes are vendored verbatim from microsandbox;
// data/msb-trust.LICENSE records the upstream version and re-sync command.
func trustHelperScript() []byte { return embeddedTrustHelperScript }
