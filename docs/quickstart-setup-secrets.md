# Quickstart: set up release signing secrets on Linux

Prepare SSH, Cosign and OpenPGP keys for a key-based Forge CI release. The names
below are the reusable workflows' secret names; a consumer can map its own
secret names to them. Configure only the secrets your chosen delivery layer
and release features require. See [Secrets and permissions](reference.md) and
the [Forgejo secret contract](forgejo.md#secrets-and-tokens).

This guide creates keys locally. It does not grant repository permissions,
configure a runner, publish a release, or create API/registry tokens.

## Prefer non-exportable keys or short-lived signing identities

For a production setup, prefer these approaches over copying long-lived private
keys into CI:

- **Hardware-backed keys:** use a hardware token, HSM, or a KMS configured with
  a non-exportable, hardware-backed key where supported. Human release-request
  signing is a good fit for a hardware token requiring presence or a PIN.
  Automated signing needs a supported device/service integration with narrowly
  scoped signing authority. A smartcard stub or an SSH security-key handle is
  not an ordinary private key that an unattended runner can import and use.
- **Sigstore keyless signing:** where the forge's workload identity is trusted,
  use OIDC with **Fulcio** for short-lived signing certificates and **Rekor**
  for transparency evidence. Verifiers must constrain both the trusted issuer
  and the intended workflow identity. Public Rekor entries are permanent and
  can disclose signing identity and artifact metadata; choose that policy
  deliberately.

These reduce long-lived key exposure; they do not make an untrusted release
workflow safe. Hardware-backed signing and keyless signing are different
approaches, not requirements to combine into one mechanism.

**Current support matters:** the GitHub layer offers keyless artifact signing,
and the CLI has explicit KMS/PKCS#11 signing paths. The current Forgejo reusable
workflows use organization-held keys and expose no KMS input. Public Fulcio
does not trust an arbitrary Forgejo issuer. A Forgejo keyless deployment needs
a trusting Sigstore service and a separately integrated workflow and verifier;
the commands below do not configure that. Setting Forgejo's
`transparency-log: public` logs signatures made with the existing Cosign key;
it does not switch the workflow to keyless signing.

See [Signing methods](verification.md#signing-methods-for-release-artifacts),
[the Forgejo signing boundary](forgejo.md#signing), and the
[OpenBao example](../examples/signing/openbao-kms/README.md). Keyless artifact
signing does not replace the current SSH Git-object signing or OpenPGP package
signatures automatically.

## 1. Prepare a private directory

Use a trusted Linux machine and an ordinary user account. Install OpenSSH's
`ssh-keygen`, GnuPG's `gpg`, and a verified Cosign 3 release through your normal
trusted installation process.

Replace `/home/operator` with your own home directory in every command, and
replace the example bot identity with your organization's identity. Use a fresh
directory outside any Git checkout. If it already exists, review and reuse the
existing keys or choose another directory; do not overwrite them casually.

```bash
umask 077
mkdir -p /home/operator/.local/share
mkdir -m 700 /home/operator/.local/share/release-keys
```

For an existing signing identity, recover its private key from its protected
backup instead of generating a replacement. A public key alone cannot recover
the private key.

## 2. Create the SSH release-bot key

The current secret-based SSH automation has no separate SSH-passphrase input.
Its software-key setup therefore uses an unencrypted private key. Treat this
as the exportable-key fallback, not as a hardware-backed setup.

```bash
ssh-keygen -t ed25519 -N '' \
  -C 'Example Release Bot <release-bot@example.org>' \
  -f /home/operator/.local/share/release-keys/release-ssh
```

- Set **`RELEASE_SSH_SIGNING_KEY`** to the complete contents of `release-ssh`,
  including its OpenSSH header, footer and newlines.
- Register **`release-ssh.pub`** with the release bot's forge account, or a
  write-capable repository deploy key where the provider supports that route.
- Give the identity only the repository write access required to push the
  prepared release commit and create final release tags. Configure branch/tag
  protection to permit those operations; the private-key secret alone grants
  no forge access. It needs no host administrator privileges.
- Set the workflow's `bot-name` and `bot-email` consistently. For the Forgejo
  layer, also configure `release-git-url` and the independently verified
  `release-git-host-key-fingerprint`.

Keep the human who requests a release separate from the bot that publishes it.
Commit authorized human public keys to the request-signature allowlist used by
your layer. Forgejo uses `release-request-allowed-signers` when
`verify-release-request-signature: 'true'`. The bot's public key also needs to
be available to verifiers of its final commits/tags; see
[Git signing](verification.md#signing-the-release-commit--tag-git-objects).

## 3. Create or recover the Cosign key pair

**If a policy repository already contains the trusted `cosign.pub`, use the
matching existing private key.** Creating another pair does not recreate that
identity, even if you use the same filename or passphrase.

For a new signing identity:

```bash
cosign generate-key-pair \
  --output-key-prefix /home/operator/.local/share/release-keys/cosign
```

Enter a strong, non-empty passphrase at the prompts. Do not put it on the command
line or in shell history.

- **`RELEASE_COSIGN_PRIVATE_KEY`**: complete contents of `cosign.key`.
- **`RELEASE_COSIGN_PASSPHRASE`**: the passphrase you entered.
- **`cosign.pub`**: public verification key; store it in the reviewed policy
  repository and supply its exact PEM text through `cosign-public-key`.
  Consumers commonly use a `COSIGN_PUBLIC_KEY` repository/organization variable.

Record the public key's digest:

```bash
sha256sum /home/operator/.local/share/release-keys/cosign.pub
```

Pass the 64-character digest, without the filename, as the literal
`cosign-public-key-sha256` input. Preserve the public key's final newline so
the variable's bytes match that digest. The public key is not a secret.

When recovering a private key, derive its public key and compare it with the
independently trusted policy file:

```bash
cosign public-key \
  --key /home/operator/.local/share/release-keys/cosign.key \
  > /home/operator/.local/share/release-keys/derived-cosign.pub
cmp /path/to/release-policy/keys/cosign.pub \
  /home/operator/.local/share/release-keys/derived-cosign.pub
```

Replace `/path/to/release-policy` with your actual policy checkout. A mismatch
must be investigated, not fixed by blindly replacing the trusted public key.
An intentional rotation updates the public key, its reviewed digest, the
private-key secrets and every consumer's trust configuration together. Retain
old public keys so existing releases remain verifiable.

## 4. Create an OpenPGP package-signing key

This key is needed when the selected release flow signs distro packages with
OpenPGP. In the Forgejo layer, `sign-distro-packages: 'true'` requires the three
GPG secrets; an archive-only consumer can explicitly disable that feature in
both preparation and signing.

Use a dedicated keyring, not your personal GPG keyring:

```bash
mkdir -m 700 /home/operator/.local/share/release-keys/gnupg
gpg --homedir /home/operator/.local/share/release-keys/gnupg \
  --quick-generate-key \
  'Example Release Bot <release-bot@example.org>' rsa4096 sign 2y
```

Choose a strong, non-empty passphrase when GnuPG's pinentry prompts you. This
creates a signing-capable RSA key with a two-year expiry; plan renewal before
then. A hardware-backed OpenPGP key needs an appropriate signing integration,
not the software-secret export below.

Display the full primary-key fingerprint:

```bash
gpg --homedir /home/operator/.local/share/release-keys/gnupg \
  --list-secret-keys --fingerprint
```

Export the private key and the public verification key to separate files:

```bash
gpg --homedir /home/operator/.local/share/release-keys/gnupg \
  --armor \
  --output /home/operator/.local/share/release-keys/release-gpg-private.asc \
  --export-secret-keys 'Example Release Bot <release-bot@example.org>'

gpg --homedir /home/operator/.local/share/release-keys/gnupg \
  --armor \
  --output /home/operator/.local/share/release-keys/release-gpg-public.asc \
  --export 'Example Release Bot <release-bot@example.org>'
```

- **`RELEASE_GPG_PRIVATE_KEY`**: complete armored private-key file.
- **`RELEASE_GPG_FINGERPRINT`**: full primary-key fingerprint without spaces,
  not the short key ID and not a subkey ID.
- **`RELEASE_GPG_PASSPHRASE`**: the passphrase chosen during generation.

Publish the public key and its trusted fingerprint through your documented
release-verification channel. Keep the generated revocation certificate from
the dedicated keyring's `openpgp-revocs.d` directory in protected backup.

## 5. Configure secrets and check the setup

| Secret | Value to enter in the forge's secret store |
| --- | --- |
| `RELEASE_SSH_SIGNING_KEY` | Entire `release-ssh` private-key file |
| `RELEASE_COSIGN_PRIVATE_KEY` | Entire encrypted `cosign.key` file |
| `RELEASE_COSIGN_PASSPHRASE` | Cosign passphrase |
| `RELEASE_GPG_PRIVATE_KEY` | Entire `release-gpg-private.asc` file |
| `RELEASE_GPG_FINGERPRINT` | Full primary-key fingerprint, without spaces |
| `RELEASE_GPG_PASSPHRASE` | GPG passphrase |

Configure these through the forge's protected repository or organization secret
settings, restricting organization secrets to the intended release repositories.
Pass them only to the signing/preparation jobs that need them, not to untrusted
pull requests or ordinary build jobs. Preserve multiline key values exactly.

Before the first release, confirm:

1. The bot's public SSH key and branch/tag permissions match the configured
   private key, and the SSH host fingerprint comes from a trusted source.
2. The Cosign private key derives the trusted public key, and that public key
   hashes to the consumer's literal digest.
3. The GPG private key's full primary fingerprint matches the configured value;
   its public key is available to package verifiers.
4. API and registry credentials are configured separately with the rights the
   chosen [delivery layer](reference.md) requires. Signing keys are not API
   tokens, and publishing a signer image does not require copying release keys
   into that image.
5. The signing runner meets the layer's isolation and
   [swap policy](verification.md#swap-policy).

Keep encrypted backups of keys, passphrases and recovery material. Do not
commit private files, print them in CI logs, or paste them into chat or issues.
Passphrases protect stored files; placing a private key and its passphrase in
the same CI secret store does not prevent a compromised signing job from using
them. This is why non-exportable hardware-backed keys or appropriately trusted
keyless signing are preferable when the integration supports them.
