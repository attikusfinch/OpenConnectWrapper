# Bundled OpenConnect for Windows amd64

This directory contains the Windows 64-bit OpenConnect command-line client extracted from the official OpenConnect Windows NSIS installer.

Source page:

- https://www.infradead.org/openconnect/packages.html

Downloaded installer:

- https://gitlab.com/openconnect/openconnect/-/jobs/artifacts/master/raw/openconnect-installer-MinGW64-GnuTLS.exe?job=MinGW64/GnuTLS

Hashes captured at download time:

```text
openconnect-installer-MinGW64-GnuTLS.exe
CFAEB652FE42EA68FA7035B9B616C4DEC0AA7F87A9D7EF1FE9D8CF425306F042

openconnect.exe
1A6B9DD39770B0CD8562DD85F853935520E3CEECC5E582602600DFEF38BD38D7
```

The application prefers this bundled `openconnect.exe` on Windows when the profile settings keep the default `openconnect` path.
