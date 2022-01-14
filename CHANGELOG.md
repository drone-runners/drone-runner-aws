# Changelog

## [v1.0.0-rc.2](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.2) (2022-01-14)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.1...v1.0.0-rc.2)

**Merged pull requests:**

- Mark DRONE\_RPC\_HOST and DRONE\_RPC\_SECRET as optional in delegate mode [\#64](https://github.com/drone-runners/drone-runner-aws/pull/64) ([vistaarjuneja](https://github.com/vistaarjuneja))

## [v1.0.0-rc.1](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.1) (2022-01-05)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-alpha.1...v1.0.0-rc.1)

**Implemented enhancements:**

- \(feat\) make lifetime settings configurable [\#62](https://github.com/drone-runners/drone-runner-aws/pull/62) ([tphoney](https://github.com/tphoney))
- support for custom cloud init template [\#60](https://github.com/drone-runners/drone-runner-aws/pull/60) ([marko-gacesa](https://github.com/marko-gacesa))
- cloud init scripts as go templates [\#59](https://github.com/drone-runners/drone-runner-aws/pull/59) ([marko-gacesa](https://github.com/marko-gacesa))
- Stream logs for VM setup in case log config is set [\#57](https://github.com/drone-runners/drone-runner-aws/pull/57) ([shubham149](https://github.com/shubham149))
- Support for aws key name & redirect logs for lite-engine [\#54](https://github.com/drone-runners/drone-runner-aws/pull/54) ([shubham149](https://github.com/shubham149))
- updated delegate API: stage ID can be used to call all delegate APIs [\#51](https://github.com/drone-runners/drone-runner-aws/pull/51) ([marko-gacesa](https://github.com/marko-gacesa))
- pool manager is a part of the delegate command [\#48](https://github.com/drone-runners/drone-runner-aws/pull/48) ([marko-gacesa](https://github.com/marko-gacesa))
- added VM pool purger [\#47](https://github.com/drone-runners/drone-runner-aws/pull/47) ([marko-gacesa](https://github.com/marko-gacesa))
- \(feat\) enable pollers for setup and step [\#39](https://github.com/drone-runners/drone-runner-aws/pull/39) ([tphoney](https://github.com/tphoney))
- Task/read certs once [\#38](https://github.com/drone-runners/drone-runner-aws/pull/38) ([eoinmcafee00](https://github.com/eoinmcafee00))
- Plug-in start step request structure directly to LE call [\#37](https://github.com/drone-runners/drone-runner-aws/pull/37) ([marko-gacesa](https://github.com/marko-gacesa))
- \(feat\) install le certs on windows [\#35](https://github.com/drone-runners/drone-runner-aws/pull/35) ([tphoney](https://github.com/tphoney))
- \(feat\) Implement LE into delegate [\#30](https://github.com/drone-runners/drone-runner-aws/pull/30) ([tphoney](https://github.com/tphoney))
- Refactoring to make the runner VM platform agnostic [\#29](https://github.com/drone-runners/drone-runner-aws/pull/29) ([marko-gacesa](https://github.com/marko-gacesa))
- \(feat\) install lite-engine on ubuntu/windows [\#27](https://github.com/drone-runners/drone-runner-aws/pull/27) ([tphoney](https://github.com/tphoney))
- \(feat\) add windows support for delegate [\#25](https://github.com/drone-runners/drone-runner-aws/pull/25) ([tphoney](https://github.com/tphoney))
- \(feat\) add lifecycle to delegate command [\#24](https://github.com/drone-runners/drone-runner-aws/pull/24) ([tphoney](https://github.com/tphoney))
- \(feat\) add pool\_owner to delegate [\#23](https://github.com/drone-runners/drone-runner-aws/pull/23) ([tphoney](https://github.com/tphoney))
- \(feat\) add delegate command [\#22](https://github.com/drone-runners/drone-runner-aws/pull/22) ([tphoney](https://github.com/tphoney))

**Fixed bugs:**

- Fix windows cloudinit file [\#61](https://github.com/drone-runners/drone-runner-aws/pull/61) ([shubham149](https://github.com/shubham149))
- \(fix\) compile command accepts aws envs [\#58](https://github.com/drone-runners/drone-runner-aws/pull/58) ([tphoney](https://github.com/tphoney))
- Fix git command visibility in cloud-init [\#56](https://github.com/drone-runners/drone-runner-aws/pull/56) ([shubham149](https://github.com/shubham149))
- \(fix\) install docker in cloud init for linux [\#53](https://github.com/drone-runners/drone-runner-aws/pull/53) ([tphoney](https://github.com/tphoney))
- improved logging and fixed timeout issues for LE calls [\#52](https://github.com/drone-runners/drone-runner-aws/pull/52) ([marko-gacesa](https://github.com/marko-gacesa))
- removed fmt print calls from delegate handler methods [\#49](https://github.com/drone-runners/drone-runner-aws/pull/49) ([marko-gacesa](https://github.com/marko-gacesa))
- update api setup parms [\#43](https://github.com/drone-runners/drone-runner-aws/pull/43) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(fix\) pass pool os to delegate setup handler [\#41](https://github.com/drone-runners/drone-runner-aws/pull/41) ([tphoney](https://github.com/tphoney))
- \(fix\) log provisioning when complete [\#21](https://github.com/drone-runners/drone-runner-aws/pull/21) ([tphoney](https://github.com/tphoney))

**Merged pull requests:**

- \(maint\) release prep for rc.1 [\#63](https://github.com/drone-runners/drone-runner-aws/pull/63) ([tphoney](https://github.com/tphoney))
- \(maint\) remove livelog [\#46](https://github.com/drone-runners/drone-runner-aws/pull/46) ([tphoney](https://github.com/tphoney))
- \(maint\) add docker manifest file [\#45](https://github.com/drone-runners/drone-runner-aws/pull/45) ([tphoney](https://github.com/tphoney))
- \(maint\) setup default pool settings [\#44](https://github.com/drone-runners/drone-runner-aws/pull/44) ([tphoney](https://github.com/tphoney))
- Refactor of VM instance pool management [\#42](https://github.com/drone-runners/drone-runner-aws/pull/42) ([marko-gacesa](https://github.com/marko-gacesa))
- \(maint\) update docs for delegate step [\#40](https://github.com/drone-runners/drone-runner-aws/pull/40) ([tphoney](https://github.com/tphoney))
- Changed API request body to match the design document [\#36](https://github.com/drone-runners/drone-runner-aws/pull/36) ([marko-gacesa](https://github.com/marko-gacesa))
- \(maint\) make aws instance creation simpler [\#34](https://github.com/drone-runners/drone-runner-aws/pull/34) ([tphoney](https://github.com/tphoney))
- \(maint\) delegate remove stage storage [\#33](https://github.com/drone-runners/drone-runner-aws/pull/33) ([tphoney](https://github.com/tphoney))
- \(maint\) move aws structs to poolfile [\#28](https://github.com/drone-runners/drone-runner-aws/pull/28) ([tphoney](https://github.com/tphoney))
- \(maint\) move code into logic groups [\#26](https://github.com/drone-runners/drone-runner-aws/pull/26) ([tphoney](https://github.com/tphoney))



\* *This Changelog was automatically generated by [github_changelog_generator](https://github.com/github-changelog-generator/github-changelog-generator)*
