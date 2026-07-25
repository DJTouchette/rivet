---
tags: [auth, authentication, session, login, jwt, oauth, sso, token]
related_paths:
  - services/auth/**
  - internal/auth/**
owner: platform-team
last_reviewed: 2026-03-02
---

# Authentication and Sessions

## Purpose
Establishes who a request is from. Authorization (what they may do) is a
separate concern owned by the policy service.

## Login flows
Two supported flows:
1. Password + TOTP, issuing a first-party session.
2. OAuth 2.0 authorization code with PKCE against the configured SSO
   provider. The provider's id_token is exchanged for a first-party session
   immediately; we never hand a provider token to downstream services.

## Sessions
A session is an opaque server-side record keyed by a random 256-bit id. The
cookie carries only the id. Access tokens minted from a session are short
lived JWTs (5 minutes) so revocation is effectively immediate.

## Revocation
Deleting the session record revokes every derived token within one JWT
lifetime. Password change and TOTP reset both revoke all sessions for the
user.

## Note
Where session records are physically stored and how they are read back at
request time is the `session-cache` module's concern, not this doc's.
