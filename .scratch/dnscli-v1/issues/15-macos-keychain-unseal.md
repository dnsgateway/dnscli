# 15: macOS Keychain + Touch ID Unseal Method

**What to build:** On macOS, the user can enroll the Keychain with Touch ID as an Unseal Method. It is another key slot wrapping the same data key, so it sits alongside the passphrase. On macOS it is the default Unseal Method, and Unseal prompts for Touch ID. The passphrase stays available for recovery and can be chosen by flag or environment variable.

**Blocked by:** 04

**Status:** ready-for-agent

- [ ] The Keychain + Touch ID Unseal Method wraps the data key in its own slot
- [ ] Enrolling it requires an already-unsealed session, for example one unsealed with the passphrase
- [ ] It is the default Unseal Method on macOS. A flag or environment variable can select the passphrase instead
- [ ] The build still works on non-macOS platforms, where the method is unavailable
- [ ] Manually verified on macOS: enroll, Unseal with Touch ID, fall back to the passphrase
