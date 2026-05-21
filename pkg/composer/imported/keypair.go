package imported

// KeyPairPublicKeyAttr is the single aws_key_pair argument that an
// imported key pair cannot reproduce: ec2:DescribeKeyPairs (and the
// AWS::EC2::KeyPair CloudControl handler) return the key fingerprint
// but NEVER the public-key material — EC2 does not expose it on read.
//
// `public_key` is REQUIRED and ForceNew on the Terraform schema, so an
// imported aws_key_pair both fails the composer's required-argument
// check and — if a value were guessed — would force-replace (destroy)
// the live key pair on the first apply. The fix mirrors the Lambda
// code adoption pattern (#652 / #665): inject a syntactically valid
// placeholder and pin `public_key` under `lifecycle { ignore_changes }`
// so terraform never acts on the placeholder.
var KeyPairPublicKeyAttr = []string{"public_key"}

// KeyPairPlaceholderPublicKey is the value pinned on
// `aws_key_pair.public_key` for an imported key pair. It is a real,
// syntactically valid ed25519 SSH public key (the AWS provider
// validates the key format), generated once at authoring time with its
// private key immediately discarded — so it is inert and corresponds
// to no usable private key. The comment field marks it unmistakably.
//
// It is always paired with `lifecycle { ignore_changes = ["public_key"] }`
// so terraform never replaces the live key pair to match this
// placeholder. Both the genconfig fixup (terraform-driven path) and the
// composer's imported.tf emitter (SDK-enrich path) inject this same
// value.
const KeyPairPlaceholderPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBYRn+c+IPlLaBIAbZvs13Vq3XdJvTtWNvMdoeR9s/Ea insideout-imported-placeholder-key"
