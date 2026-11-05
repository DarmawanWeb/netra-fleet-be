# Trusted license keys

netra-api accepts a license only when it is signed by one of the Ed25519 public keys in this
directory (`*.pub`, the base64 key on one line). The keys are compiled into the binary, so a
license cannot be accepted by changing the server's configuration.

- Make a key pair once, on the machine that issues licenses, never on a server:
  `go run ./cmd/netra-license keygen -out ~/netra-keys/ezra`. It writes `ezra.key` (private,
  mode 0600) and `ezra.pub`.
- Commit only the `.pub` here. The private key never goes into a repository, an image or a
  server; whoever has it can issue any license. Keep it offline with a backup.
- To replace a key, add the new `.pub`, issue new licenses with it, and remove the old `.pub`
  once every client has its new license.

See [licensing](../../../docs/licensing.md).
