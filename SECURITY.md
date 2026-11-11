# Security Policy

## Supported versions

Security fixes are applied to the latest `main` branch.

## Reporting a vulnerability

Please do not open a public issue for security-sensitive reports. Use GitHub's private security advisory feature for this repository:

1. open the repository's **Security** tab;
2. choose **Advisories**;
3. create a private advisory.

Include the affected version or commit, a minimal reproduction, and the impact. Do not include real API keys, management keys, provider credentials, or production request data.

## Scope

TokenSaver is a local request transformer. It does not manage provider credentials, route accounts, or store request bodies. The main security boundaries are:

- management API authentication is provided by the host;
- plugin and data directories should have restrictive permissions;
- management endpoints should not be exposed to untrusted networks;
- gateway upstream URLs must not contain embedded credentials;
- lossy engines should only be enabled after evaluating output quality.
