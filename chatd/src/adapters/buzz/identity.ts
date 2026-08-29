import { createHash } from "node:crypto";

// The canonical definition of this derivation is internkim's
// internal/buzzidentity/identity.go Secret; chatd is a conformance consumer of
// it and the shared vectors in tests/buzz-identity.test.ts prove the two agree.
export function deriveBuzzSecret(seed: string, email: string): string {
	return createHash("sha256")
		.update(`${seed}|secret|${email.trim().toLowerCase()}`)
		.digest("hex");
}
