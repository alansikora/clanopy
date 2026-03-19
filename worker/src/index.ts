import * as jose from "jose";

interface Env {
  APP_ID: string;
  APP_PRIVATE_KEY: string;
}

const GITHUB_OIDC_ISSUER = "https://token.actions.githubusercontent.com";
const GITHUB_API = "https://api.github.com";

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204 });
    }

    if (request.method !== "POST" || new URL(request.url).pathname !== "/token") {
      return Response.json({ error: "Not found" }, { status: 404 });
    }

    try {
      // 1. Extract OIDC token from Authorization header.
      const authHeader = request.headers.get("Authorization");
      if (!authHeader?.startsWith("Bearer ")) {
        return Response.json({ error: "Missing Authorization header" }, { status: 401 });
      }
      const oidcToken = authHeader.slice(7);

      // 2. Verify OIDC token against GitHub's JWKS.
      const JWKS = jose.createRemoteJWKSet(
        new URL(`${GITHUB_OIDC_ISSUER}/.well-known/jwks`)
      );

      const { payload } = await jose.jwtVerify(oidcToken, JWKS, {
        issuer: GITHUB_OIDC_ISSUER,
        audience: "clanopy",
      });

      // 3. Extract repository from OIDC claims.
      const repository = payload.repository as string | undefined;
      if (!repository) {
        return Response.json({ error: "No repository claim in OIDC token" }, { status: 400 });
      }

      // 4. Generate GitHub App JWT.
      const appJwt = await generateAppJwt(env.APP_ID, env.APP_PRIVATE_KEY);

      // 5. Find the installation for this repository.
      const installationId = await findInstallation(appJwt, repository);
      if (!installationId) {
        return Response.json(
          { error: `Clanopy Review app is not installed on ${repository}` },
          { status: 404 }
        );
      }

      // 6. Generate a scoped installation token.
      const [owner, repo] = repository.split("/");
      const tokenResponse = await fetch(
        `${GITHUB_API}/app/installations/${installationId}/access_tokens`,
        {
          method: "POST",
          headers: {
            Authorization: `Bearer ${appJwt}`,
            Accept: "application/vnd.github+json",
            "User-Agent": "clanopy-token-proxy",
          },
          body: JSON.stringify({
            repositories: [repo],
            permissions: { pull_requests: "write", contents: "read" },
          }),
        }
      );

      if (!tokenResponse.ok) {
        const body = await tokenResponse.text();
        return Response.json(
          { error: `Failed to create installation token: ${body}` },
          { status: 502 }
        );
      }

      const tokenData = (await tokenResponse.json()) as { token: string };
      return Response.json({ token: tokenData.token });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Unknown error";
      return Response.json({ error: message }, { status: 500 });
    }
  },
};

async function generateAppJwt(appId: string, privateKeyPem: string): Promise<string> {
  // GitHub generates PKCS#1 keys ("BEGIN RSA PRIVATE KEY"), but jose
  // requires PKCS#8 ("BEGIN PRIVATE KEY"). Convert if needed.
  const pem = pkcs1ToPkcs8(privateKeyPem);
  const privateKey = await jose.importPKCS8(pem, "RS256");
  const now = Math.floor(Date.now() / 1000);

  return new jose.SignJWT({})
    .setProtectedHeader({ alg: "RS256" })
    .setIssuer(appId)
    .setIssuedAt(now - 60)
    .setExpirationTime(now + 600)
    .sign(privateKey);
}

function pkcs1ToPkcs8(pem: string): string {
  // Already PKCS#8.
  if (pem.includes("BEGIN PRIVATE KEY")) {
    return pem;
  }
  // Convert PKCS#1 DER to PKCS#8 DER by wrapping with the RSA algorithm OID.
  const b64 = pem
    .replace(/-----BEGIN RSA PRIVATE KEY-----/, "")
    .replace(/-----END RSA PRIVATE KEY-----/, "")
    .replace(/\s/g, "");
  const pkcs1Der = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));

  // PKCS#8 wraps the PKCS#1 key in a SEQUENCE { AlgorithmIdentifier, OCTET STRING }.
  // AlgorithmIdentifier for RSA: SEQUENCE { OID 1.2.840.113549.1.1.1, NULL }
  const algorithmId = new Uint8Array([
    0x30, 0x0d, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01,
    0x01, 0x05, 0x00,
  ]);

  // Build OCTET STRING wrapping the PKCS#1 key.
  const octetString = wrapAsn1(0x04, pkcs1Der);

  // Build the version INTEGER (0).
  const version = new Uint8Array([0x02, 0x01, 0x00]);

  // Build outer SEQUENCE.
  const inner = concat(version, algorithmId, octetString);
  const pkcs8Der = wrapAsn1(0x30, inner);

  const pkcs8B64 = btoa(String.fromCharCode(...pkcs8Der));
  const lines = pkcs8B64.match(/.{1,64}/g) || [];
  return `-----BEGIN PRIVATE KEY-----\n${lines.join("\n")}\n-----END PRIVATE KEY-----`;
}

function wrapAsn1(tag: number, data: Uint8Array): Uint8Array {
  const len = encodeAsn1Length(data.length);
  const result = new Uint8Array(1 + len.length + data.length);
  result[0] = tag;
  result.set(len, 1);
  result.set(data, 1 + len.length);
  return result;
}

function encodeAsn1Length(length: number): Uint8Array {
  if (length < 0x80) {
    return new Uint8Array([length]);
  }
  const bytes: number[] = [];
  let temp = length;
  while (temp > 0) {
    bytes.unshift(temp & 0xff);
    temp >>= 8;
  }
  return new Uint8Array([0x80 | bytes.length, ...bytes]);
}

function concat(...arrays: Uint8Array[]): Uint8Array {
  const total = arrays.reduce((sum, a) => sum + a.length, 0);
  const result = new Uint8Array(total);
  let offset = 0;
  for (const a of arrays) {
    result.set(a, offset);
    offset += a.length;
  }
  return result;
}

async function findInstallation(
  appJwt: string,
  repository: string
): Promise<number | null> {
  // Try the direct repo installation endpoint first.
  const response = await fetch(
    `${GITHUB_API}/repos/${repository}/installation`,
    {
      headers: {
        Authorization: `Bearer ${appJwt}`,
        Accept: "application/vnd.github+json",
        "User-Agent": "clanopy-token-proxy",
      },
    }
  );

  if (response.ok) {
    const data = (await response.json()) as { id: number };
    return data.id;
  }

  return null;
}
