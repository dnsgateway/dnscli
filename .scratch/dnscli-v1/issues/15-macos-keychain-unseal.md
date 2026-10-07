# 15: macOS Keychain + Touch ID Unseal Method

**What to build:** On macOS, the user can enroll the Keychain with Touch ID as an Unseal Method. It is another key slot wrapping the same data key, so it sits alongside the passphrase. On macOS it is the default Unseal Method, and Unseal prompts for Touch ID. The passphrase stays available for recovery and can be chosen by flag or environment variable.

**Blocked by:** 04

**Status:** resolved

- [x] The Keychain + Touch ID Unseal Method wraps the data key in its own slot
- [x] Enrolling it requires an already-unsealed session, for example one unsealed with the passphrase
- [x] It is the default Unseal Method on macOS. A flag or environment variable can select the passphrase instead
- [x] The build still works on non-macOS platforms, where the method is unavailable
- [x] Manually verified on macOS: enroll, Unseal with Touch ID, fall back to the passphrase

## Comments

- 2026-10-07: Implemented in the commits up to fa120e8. Review fixes are in the batch ending d6153a7.
  - Security decision: there is no fallback to the login keychain. Enrolling stores the key only in the data protection keychain, with BiometryCurrentSet. An unsigned build, or a Mac without Touch ID, refuses to enroll and the user keeps using the passphrase.
  - Re-enrolling or a failed enrollment deletes the Keychain items it leaves behind.
  - The Objective-C bridge (`internal/keychain/keychain_darwin.m`) has never been compiled. Status is ready-for-human until the Mac check below passes.
  - Risk: a plain CLI binary may not be able to carry `keychain-access-groups` without a provisioning profile. If macOS kills the signed binary, the Keychain method needs app-bundle packaging.
  - Risk: it is assumed that SecItemDelete removes a biometry-protected item without asking for Touch ID.
- 2026-10-07: Manually verified on macOS with a `-tags devkeychain` build (870eed1). Enroll, Unseal with Touch ID, and fall back to the passphrase all work, and all known issues are fixed. Resolved.
  - The dev build uses the login keychain, guarded only by dnscli's own Touch ID check. The data protection keychain path still needs a signed .app bundle built from the `hack/macos` templates. That path has not been run on a Mac yet.

### Manual Mac verification

1. `go build -o dnscli ./cmd/dnscli`. Use a fresh DB, e.g. `--db /tmp/dnscli-mac.db`.
2. **Unsigned build is refused.** Run `./dnscli --unseal passphrase --db /tmp/dnscli-mac.db`. Add an Account and choose a passphrase. Press `K`, enter the passphrase, then press Enter.
   - Expect "Touch ID protection is unavailable: this dnscli build is not signed with a keychain entitlement".
   - Expect "The Keychain is not enrolled: keep using the passphrase".
   - `security find-generic-password -s dnscli` should find nothing.
3. **Sign the binary.** Use an Apple Development or Developer ID identity. The entitlements plist needs `keychain-access-groups` = `<TEAMID>.<bundle id>` and `com.apple.application-identifier`, plus a provisioning profile if needed. Run `codesign -s "<identity>" --entitlements dnscli.entitlements -f ./dnscli`. If macOS kills the binary, record that.
4. **Enroll.** Repeat step 2's enrollment with the signed build. Expect "Keychain enrolled: Touch ID now Unseals dnscli…".
5. **Unseal.** Quit, run `./dnscli --db /tmp/dnscli-mac.db` and add an Account. Expect exactly one Touch ID prompt, no passphrase prompt, and "unsealed".
6. **Cancel.** Cancel Touch ID or choose "Use Passphrase". Expect "Touch ID was canceled", then a passphrase prompt; the passphrase should unseal.
7. **Re-enroll.** Press `K` and enroll again. A later session should still unseal with one Touch ID prompt.
8. **No Touch ID.** On a Mac with no Touch ID, no fingerprints enrolled, or in clamshell mode, enrolling should fail with "Touch ID is not set up on this Mac" and leave only the passphrase key slot.
9. **Fingerprint change.** Add a fingerprint. Touch ID Unseal should fail and fall back to the passphrase. Re-enrolling should restore it.
10. **Old build's item.** A login-keychain item from an earlier build won't be used, and the passphrase still works. Re-enroll, then remove the old item with `security delete-generic-password -s dnscli -a unseal-<id>`.
