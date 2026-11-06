# Changelog

## [0.19.0](https://github.com/elee1766/acphost/compare/v0.18.1...v0.19.0) (2026-11-06)


### Features

* **acphost:** start a detached host in a given cgroup ([#223](https://github.com/elee1766/acphost/issues/223)) ([#224](https://github.com/elee1766/acphost/issues/224)) ([27be17f](https://github.com/elee1766/acphost/commit/27be17f2828a10022fd6493103ac911cfd939766))

## [0.18.1](https://github.com/elee1766/acphost/compare/v0.18.0...v0.18.1) (2026-11-06)


### Bug fixes

* **acphost:** release host.lock before supervisor.lock ([#216](https://github.com/elee1766/acphost/issues/216)) ([#217](https://github.com/elee1766/acphost/issues/217)) ([3a1bfc5](https://github.com/elee1766/acphost/commit/3a1bfc589812e819f1f9413ec5b58f032aa8a4da))

## [0.18.0](https://github.com/elee1766/acphost/compare/v0.17.0...v0.18.0) (2026-11-06)


### Features

* **cli:** start --host runs an ACP host detached ([#211](https://github.com/elee1766/acphost/issues/211)) ([#214](https://github.com/elee1766/acphost/issues/214)) ([80f9f04](https://github.com/elee1766/acphost/commit/80f9f04334d887bf1fc100813e5192b711103283))

## [0.17.0](https://github.com/elee1766/acphost/compare/v0.16.1...v0.17.0) (2026-11-06)


### Features

* **acphost:** run the host on proc's detached supervisor ([#210](https://github.com/elee1766/acphost/issues/210)) ([#212](https://github.com/elee1766/acphost/issues/212)) ([c8419f7](https://github.com/elee1766/acphost/commit/c8419f76aad32e78cfcefe80a65e7a70bb939f7e))

## [0.16.1](https://github.com/elee1766/acphost/compare/v0.16.0...v0.16.1) (2026-11-06)


### Bug fixes

* **reexec:** lock the userns helper to one thread before clearing caps ([#204](https://github.com/elee1766/acphost/issues/204)) ([#206](https://github.com/elee1766/acphost/issues/206)) ([9a53833](https://github.com/elee1766/acphost/commit/9a53833135fd4c49539ffe70b9a01bab45adbb39))

## [0.16.0](https://github.com/elee1766/acphost/compare/v0.15.0...v0.16.0) (2026-11-06)


### Features

* **cli:** detached mode start, status, stop, wait ([#200](https://github.com/elee1766/acphost/issues/200)) ([#203](https://github.com/elee1766/acphost/issues/203)) ([bb59a83](https://github.com/elee1766/acphost/commit/bb59a837e4354bbdbd773ccf88414b0f5daa08f8))

## [0.15.0](https://github.com/elee1766/acphost/compare/v0.14.0...v0.15.0) (2026-11-06)


### Features

* **userns:** map the caller as ns root so bwrap mounts as the controller ([#162](https://github.com/elee1766/acphost/issues/162)) ([#189](https://github.com/elee1766/acphost/issues/189)) ([632f038](https://github.com/elee1766/acphost/commit/632f038bfc24c3b28b4699d34708e603647a5d63))

## [0.14.0](https://github.com/elee1766/acphost/compare/v0.13.0...v0.14.0) (2026-11-06)


### ⚠ BREAKING CHANGES

* **acphost:** Go callers must use the sandbox, cgroup and userns packages. The JSON wire format is unchanged (same types), so no Protocol bump. jobshop no longer uses the aliases (elee1766/jobshop#793).

### refactor

* **acphost:** remove root isolation type aliases ([#160](https://github.com/elee1766/acphost/issues/160)) ([#192](https://github.com/elee1766/acphost/issues/192)) ([da18728](https://github.com/elee1766/acphost/commit/da18728448c1963d439993af5fc94d97b2c3b282))

## [0.13.0](https://github.com/elee1766/acphost/compare/v0.12.1...v0.13.0) (2026-11-06)


### Features

* **cmd:** add acphost CLI with run and host modes from a YAML config ([#165](https://github.com/elee1766/acphost/issues/165)) ([#182](https://github.com/elee1766/acphost/issues/182)) ([b762aca](https://github.com/elee1766/acphost/commit/b762aca4b9c869cdb22ccffe62de9c589d69f650))

## [0.12.1](https://github.com/elee1766/acphost/compare/v0.12.0...v0.12.1) (2026-11-06)


### Features

* **proc:** add StartDetached and Open for commands that outlive the caller ([#159](https://github.com/elee1766/acphost/issues/159)) ([#177](https://github.com/elee1766/acphost/issues/177)) ([43b76f9](https://github.com/elee1766/acphost/commit/43b76f98ef85c6477c34aa902c4c94a246cfe5a4))

## [0.12.0](https://github.com/elee1766/acphost/compare/v0.11.3...v0.12.0) (2026-11-06)


### ⚠ BREAKING CHANGES

* **pkg:** import paths moved under pkg/ (github.com/elee1766/acphost -> github.com/elee1766/acphost/pkg/acphost, github.com/elee1766/acphost/proc -> .../pkg/proc, and so on). Old to new table and the pkg/acphost vs pkg/host choice: docs/notes/164-pkg-layout.md.

### refactor

* **pkg:** move packages under pkg/ and drop internal/ ([#164](https://github.com/elee1766/acphost/issues/164)) ([#175](https://github.com/elee1766/acphost/issues/175)) ([2bfe982](https://github.com/elee1766/acphost/commit/2bfe9820be195fff6b3cce3a4f6efd51ab968767))

## [0.11.3](https://github.com/elee1766/acphost/compare/v0.11.2...v0.11.3) (2026-11-06)


### Features

* **proc:** add proc.Start and run the host agent through it ([#158](https://github.com/elee1766/acphost/issues/158)) ([#172](https://github.com/elee1766/acphost/issues/172)) ([b3d8b36](https://github.com/elee1766/acphost/commit/b3d8b36af72fcc9564efdfc012620ba253f5734c))

## [0.11.2](https://github.com/elee1766/acphost/compare/v0.11.1...v0.11.2) (2026-11-06)


### Features

* **acphost:** join existing namespaces with Spec.Join ([#166](https://github.com/elee1766/acphost/issues/166)) ([#171](https://github.com/elee1766/acphost/issues/171)) ([9611627](https://github.com/elee1766/acphost/commit/96116274dd612bbf2bfabf898703cfe18e0a6fb3))

## [0.11.1](https://github.com/elee1766/acphost/compare/v0.11.0...v0.11.1) (2026-11-06)


### Features

* **sandbox:** overlay mounts merging layer dirs onto one target ([#163](https://github.com/elee1766/acphost/issues/163)) ([#168](https://github.com/elee1766/acphost/issues/168)) ([223329a](https://github.com/elee1766/acphost/commit/223329a34327c00b959ffe3868a9275b7e25d84a))

## [0.11.0](https://github.com/elee1766/acphost/compare/v0.10.0...v0.11.0) (2026-11-06)


### ⚠ BREAKING CHANGES

* **proc:** callers (jobshop main, TestMains using user or pasta) must call proc.MaybeChild().

### Features

* **proc:** add maybechild re-exec helpers with fd config, no init() ([#157](https://github.com/elee1766/acphost/issues/157)) ([#167](https://github.com/elee1766/acphost/issues/167)) ([eadf29e](https://github.com/elee1766/acphost/commit/eadf29e95b9441733d2baac7c2392bbd1bdacf49))

## [0.10.0](https://github.com/elee1766/acphost/compare/v0.9.0...v0.10.0) (2026-11-06)


### Bug fixes

* **release:** notes and release-as 0.10.0 ([#151](https://github.com/elee1766/acphost/issues/151)) ([#152](https://github.com/elee1766/acphost/issues/152)) ([04f98b5](https://github.com/elee1766/acphost/commit/04f98b56d60be2cbe77eb9f7bcbd763ec819b028))

## [0.9.0](https://github.com/elee1766/acphost/compare/v0.8.8...v0.9.0) (2026-11-06)


### Bug fixes

* **release:** trigger the 0.9.0 release ([#145](https://github.com/elee1766/acphost/issues/145)) ([#146](https://github.com/elee1766/acphost/issues/146)) ([f239f84](https://github.com/elee1766/acphost/commit/f239f8432c96fac2f5979847a5414158f6145204))

## [0.8.8](https://github.com/elee1766/acphost/compare/v0.8.7...v0.8.8) (2026-11-05)


### Performance

* **journal:** follow with inotify instead of 200ms polling ([#137](https://github.com/elee1766/acphost/issues/137)) ([#141](https://github.com/elee1766/acphost/issues/141)) ([bdaf00c](https://github.com/elee1766/acphost/commit/bdaf00c0c19908579c9c6b8068fe284aea8a44a0))

## [0.8.7](https://github.com/elee1766/acphost/compare/v0.8.6...v0.8.7) (2026-11-05)


### Features

* **preflight:** check nft when sandbox deny is set ([#135](https://github.com/elee1766/acphost/issues/135)) ([#138](https://github.com/elee1766/acphost/issues/138)) ([abf00e6](https://github.com/elee1766/acphost/commit/abf00e65249ee2d5ebcca550df3d14bc3a821a8f))

## [0.8.6](https://github.com/elee1766/acphost/compare/v0.8.5...v0.8.6) (2026-11-05)


### Features

* **preflight:** opt-in sandbox.dns check via WithDNSCheck ([#136](https://github.com/elee1766/acphost/issues/136)) ([#139](https://github.com/elee1766/acphost/issues/139)) ([2e30c93](https://github.com/elee1766/acphost/commit/2e30c93d4ff90b1b798abdd4fb812617584bf345))

## [0.8.5](https://github.com/elee1766/acphost/compare/v0.8.4...v0.8.5) (2026-11-05)


### Features

* **acphosttest:** add StartDir for a dir whose spec is already written ([#127](https://github.com/elee1766/acphost/issues/127)) ([#129](https://github.com/elee1766/acphost/issues/129)) ([45b3e35](https://github.com/elee1766/acphost/commit/45b3e35c7b800abdf8c9bb2af752aea5869cbad8))
* **sandbox:** pasta dns forward and resolv_conf settings ([#124](https://github.com/elee1766/acphost/issues/124)) ([#128](https://github.com/elee1766/acphost/issues/128)) ([bd1f5ea](https://github.com/elee1766/acphost/commit/bd1f5eaf85655ee61ee00c7d2777cf2deb25f303))


### Bug fixes

* **sandbox:** make TestSandboxPastaDNS use only a fake resolver ([#131](https://github.com/elee1766/acphost/issues/131)) ([#133](https://github.com/elee1766/acphost/issues/133)) ([5474d93](https://github.com/elee1766/acphost/commit/5474d9314ee3f58b36e7881780793954b9b6607e))

## [0.8.4](https://github.com/elee1766/acphost/compare/v0.8.3...v0.8.4) (2026-11-05)


### Features

* **preflight:** report each isolation host config gap with a fix hint ([#115](https://github.com/elee1766/acphost/issues/115)) ([#122](https://github.com/elee1766/acphost/issues/122)) ([a1f50b7](https://github.com/elee1766/acphost/commit/a1f50b7b15003844745537df56faa1cc7b6f73df))

## [0.8.3](https://github.com/elee1766/acphost/compare/v0.8.2...v0.8.3) (2026-11-05)


### Features

* **user:** check the whole mapped subuid/subgid range ([#113](https://github.com/elee1766/acphost/issues/113)) ([#118](https://github.com/elee1766/acphost/issues/118)) ([7cbaf43](https://github.com/elee1766/acphost/commit/7cbaf4399f3971b639dc7435ab5078995752eaef))

## [0.8.2](https://github.com/elee1766/acphost/compare/v0.8.1...v0.8.2) (2026-11-05)


### Features

* **acphosttest:** in-process test host helper ([#114](https://github.com/elee1766/acphost/issues/114)) ([#117](https://github.com/elee1766/acphost/issues/117)) ([47a5183](https://github.com/elee1766/acphost/commit/47a518386cd60baf24db280a64672b11ecc6111b))

## [0.8.1](https://github.com/elee1766/acphost/compare/v0.8.0...v0.8.1) (2026-11-05)


### Performance

* **journal:** read JournalTail from the end of the journal ([#112](https://github.com/elee1766/acphost/issues/112)) ([#116](https://github.com/elee1766/acphost/issues/116)) ([4188828](https://github.com/elee1766/acphost/commit/418882851ff0339e7bc566a685e2bfdf23684fbf))

## [0.8.0](https://github.com/elee1766/acphost/compare/v0.7.0...v0.8.0) (2026-11-05)


### Features

* **host:** report spawn setup failures as a typed error ([#100](https://github.com/elee1766/acphost/issues/100)) ([#104](https://github.com/elee1766/acphost/issues/104)) ([ee679a3](https://github.com/elee1766/acphost/commit/ee679a3899c70e2a454517a7afc7f9e88e4b9ab6))
* **isolation:** record pdeath helper in isolation.json ([#101](https://github.com/elee1766/acphost/issues/101)) ([#102](https://github.com/elee1766/acphost/issues/102)) ([d6dbea2](https://github.com/elee1766/acphost/commit/d6dbea2434e58c4373aef85218a500ec5b4d6c02))

## [0.7.0](https://github.com/elee1766/acphost/compare/v0.6.0...v0.7.0) (2026-11-05)


### Bug fixes

* agent outliving host under pasta on ubuntu-26.04 ([#76](https://github.com/elee1766/acphost/issues/76)) ([#92](https://github.com/elee1766/acphost/issues/92)) ([586e09c](https://github.com/elee1766/acphost/commit/586e09c0f7ddeeb824b7b42341312984338e665d))
