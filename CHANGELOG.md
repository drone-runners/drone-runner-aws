# Changelog

## [v1.0.0-rc.14](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.14) (2022-10-31)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.13...v1.0.0-rc.14)

**Implemented enhancements:**

- \(feat\): upgrade dlite version to RC1 release [\#190](https://github.com/drone-runners/drone-runner-aws/pull/190) ([vistaarjuneja](https://github.com/vistaarjuneja))

**Fixed bugs:**

- \(fix\) improve destroy logs on azure [\#188](https://github.com/drone-runners/drone-runner-aws/pull/188) ([tphoney](https://github.com/tphoney))
- \(fix\) better error messages when parsing pool file, azure especially [\#187](https://github.com/drone-runners/drone-runner-aws/pull/187) ([tphoney](https://github.com/tphoney))
- \(fix\) single bind point for pipeline base folder [\#184](https://github.com/drone-runners/drone-runner-aws/pull/184) ([tphoney](https://github.com/tphoney))

**Merged pull requests:**

- fixes issue were mac builds werent' pointing at correct pool [\#185](https://github.com/drone-runners/drone-runner-aws/pull/185) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(feat\) Inject split test binary for Linux VMs \(CIE\) [\#156](https://github.com/drone-runners/drone-runner-aws/pull/156) ([rutvijmehta-harness](https://github.com/rutvijmehta-harness))

## [v1.0.0-rc.13](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.13) (2022-09-26)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.12...v1.0.0-rc.13)

**Implemented enhancements:**

- Upgrade lite-engine version and go.mod [\#183](https://github.com/drone-runners/drone-runner-aws/pull/183) ([shubham149](https://github.com/shubham149))
- Set machine environment variables to lite-engine [\#182](https://github.com/drone-runners/drone-runner-aws/pull/182) ([shubham149](https://github.com/shubham149))
- Add support for passing fully qualified subnet and network [\#181](https://github.com/drone-runners/drone-runner-aws/pull/181) ([shubham149](https://github.com/shubham149))
- add /healthz endpoint in dlite mode [\#180](https://github.com/drone-runners/drone-runner-aws/pull/180) ([vistaarjuneja](https://github.com/vistaarjuneja))
- Reduce deadlock time in vm setup [\#179](https://github.com/drone-runners/drone-runner-aws/pull/179) ([shubham149](https://github.com/shubham149))
- \(fix\): update go mod [\#177](https://github.com/drone-runners/drone-runner-aws/pull/177) ([vistaarjuneja](https://github.com/vistaarjuneja))
- \(fix\): update go.mod [\#176](https://github.com/drone-runners/drone-runner-aws/pull/176) ([vistaarjuneja](https://github.com/vistaarjuneja))
- \(fix\): improve logging [\#175](https://github.com/drone-runners/drone-runner-aws/pull/175) ([vistaarjuneja](https://github.com/vistaarjuneja))
- \(fix\): update go.mod [\#174](https://github.com/drone-runners/drone-runner-aws/pull/174) ([vistaarjuneja](https://github.com/vistaarjuneja))
- \(feat\): make dlite logs compatible with stackdriver [\#172](https://github.com/drone-runners/drone-runner-aws/pull/172) ([vistaarjuneja](https://github.com/vistaarjuneja))
- \(feat\) make poll interval and number of executors configurable [\#171](https://github.com/drone-runners/drone-runner-aws/pull/171) ([vistaarjuneja](https://github.com/vistaarjuneja))
- Add curl tool to the docker image [\#170](https://github.com/drone-runners/drone-runner-aws/pull/170) ([shubham149](https://github.com/shubham149))

**Fixed bugs:**

- Retries on setting tag and add timeout for setup [\#178](https://github.com/drone-runners/drone-runner-aws/pull/178) ([shubham149](https://github.com/shubham149))

## [v1.0.0-rc.12](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.12) (2022-09-08)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.11...v1.0.0-rc.12)

**Implemented enhancements:**

- Update lite-engine version to 0.4.1 [\#168](https://github.com/drone-runners/drone-runner-aws/pull/168) ([shubham149](https://github.com/shubham149))
- \(feat\) add maintenance support in dlite mode [\#165](https://github.com/drone-runners/drone-runner-aws/pull/165) ([vistaarjuneja](https://github.com/vistaarjuneja))
- Add tags to a build instance [\#164](https://github.com/drone-runners/drone-runner-aws/pull/164) ([shubham149](https://github.com/shubham149))

**Fixed bugs:**

- Fix default network for gcp [\#167](https://github.com/drone-runners/drone-runner-aws/pull/167) ([shubham149](https://github.com/shubham149))
- Fix zone in gcp runner & add extra configs [\#166](https://github.com/drone-runners/drone-runner-aws/pull/166) ([shubham149](https://github.com/shubham149))

**Merged pull requests:**

- rc-12 prep [\#169](https://github.com/drone-runners/drone-runner-aws/pull/169) ([shubham149](https://github.com/shubham149))

## [v1.0.0-rc.11](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.11) (2022-08-18)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.10...v1.0.0-rc.11)

**Fixed bugs:**

- \(fix\) fix docker entry point [\#162](https://github.com/drone-runners/drone-runner-aws/pull/162) ([tphoney](https://github.com/tphoney))

**Merged pull requests:**

- \(maint\) rc11 release prep [\#163](https://github.com/drone-runners/drone-runner-aws/pull/163) ([tphoney](https://github.com/tphoney))

## [v1.0.0-rc.10](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.10) (2022-08-17)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.9...v1.0.0-rc.10)

**Implemented enhancements:**

- \(feat\) auto setup for Anka build [\#161](https://github.com/drone-runners/drone-runner-aws/pull/161) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(feat\) update manifest to include arm64 build [\#158](https://github.com/drone-runners/drone-runner-aws/pull/158) ([eoinmcafee00](https://github.com/eoinmcafee00))

**Merged pull requests:**

- \(maint\) rc10 release [\#160](https://github.com/drone-runners/drone-runner-aws/pull/160) ([tphoney](https://github.com/tphoney))
- \(maint\) formalise binary naming [\#159](https://github.com/drone-runners/drone-runner-aws/pull/159) ([tphoney](https://github.com/tphoney))

## [v1.0.0-rc.9](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.9) (2022-08-15)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.8...v1.0.0-rc.9)

**Implemented enhancements:**

- start publishing tags based on supported os/arch [\#153](https://github.com/drone-runners/drone-runner-aws/pull/153) ([vistaarjuneja](https://github.com/vistaarjuneja))
- \(feat\) windows support for azure [\#149](https://github.com/drone-runners/drone-runner-aws/pull/149) ([tphoney](https://github.com/tphoney))
- \(feat\) adds anka build provider [\#148](https://github.com/drone-runners/drone-runner-aws/pull/148) ([eoinmcafee00](https://github.com/eoinmcafee00))
- send back delegate info as part of task response [\#145](https://github.com/drone-runners/drone-runner-aws/pull/145) ([vistaarjuneja](https://github.com/vistaarjuneja))
- \(feat\) azure support for linux [\#139](https://github.com/drone-runners/drone-runner-aws/pull/139) ([eoinmcafee00](https://github.com/eoinmcafee00))

**Fixed bugs:**

- Fix cleanup of instances [\#155](https://github.com/drone-runners/drone-runner-aws/pull/155) ([shubham149](https://github.com/shubham149))
- \(bug\) fixes issue were stage owner was never getting inserted [\#152](https://github.com/drone-runners/drone-runner-aws/pull/152) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(bug\) - fixes issue were progress getting mapped wrong type on anka side [\#151](https://github.com/drone-runners/drone-runner-aws/pull/151) ([eoinmcafee00](https://github.com/eoinmcafee00))
- Cleanup pools on dlite termination [\#150](https://github.com/drone-runners/drone-runner-aws/pull/150) ([shubham149](https://github.com/shubham149))
- Fix instance name in gcp runner [\#144](https://github.com/drone-runners/drone-runner-aws/pull/144) ([shubham149](https://github.com/shubham149))
- Fix support for gcp json key credentials [\#143](https://github.com/drone-runners/drone-runner-aws/pull/143) ([shubham149](https://github.com/shubham149))

**Merged pull requests:**

- \(maint\) rc9 prep [\#157](https://github.com/drone-runners/drone-runner-aws/pull/157) ([tphoney](https://github.com/tphoney))
- \(DRON-406\) use binaries for github release on mac [\#154](https://github.com/drone-runners/drone-runner-aws/pull/154) ([tphoney](https://github.com/tphoney))
- update README for running the runner in delegate-less mode [\#147](https://github.com/drone-runners/drone-runner-aws/pull/147) ([vistaarjuneja](https://github.com/vistaarjuneja))
- add return [\#146](https://github.com/drone-runners/drone-runner-aws/pull/146) ([vistaarjuneja](https://github.com/vistaarjuneja))
- update go.mod [\#142](https://github.com/drone-runners/drone-runner-aws/pull/142) ([vistaarjuneja](https://github.com/vistaarjuneja))
- moved token creation and updation to dlite [\#141](https://github.com/drone-runners/drone-runner-aws/pull/141) ([vistaarjuneja](https://github.com/vistaarjuneja))

## [v1.0.0-rc.8](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.8) (2022-07-18)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.7...v1.0.0-rc.8)

**Fixed bugs:**

- \(bug\) delegate - fixes issue assigning stage-owner to pool even provisioning failed. [\#140](https://github.com/drone-runners/drone-runner-aws/pull/140) ([eoinmcafee00](https://github.com/eoinmcafee00))

## [v1.0.0-rc.7](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.7) (2022-07-13)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.6...v1.0.0-rc.7)

**Implemented enhancements:**

- \(DRON-318\) add auto pooling [\#135](https://github.com/drone-runners/drone-runner-aws/pull/135) ([tphoney](https://github.com/tphoney))
- \(feat\) build and publish m1 binary [\#133](https://github.com/drone-runners/drone-runner-aws/pull/133) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(feat\) initial attempt at adding remote debug [\#130](https://github.com/drone-runners/drone-runner-aws/pull/130) ([tphoney](https://github.com/tphoney))
- \(feat\) initial implementation of the digitalocean driver [\#127](https://github.com/drone-runners/drone-runner-aws/pull/127) ([tphoney](https://github.com/tphoney))

**Fixed bugs:**

- \(fix\) remove duplicate vpc from amazon [\#124](https://github.com/drone-runners/drone-runner-aws/pull/124) ([tphoney](https://github.com/tphoney))

**Merged pull requests:**

- prep for v1.0.0-rc.7 [\#138](https://github.com/drone-runners/drone-runner-aws/pull/138) ([vistaarjuneja](https://github.com/vistaarjuneja))
- add extra checks [\#137](https://github.com/drone-runners/drone-runner-aws/pull/137) ([shubham149](https://github.com/shubham149))
- Add task implementations for by-passing the java delegate [\#136](https://github.com/drone-runners/drone-runner-aws/pull/136) ([vistaarjuneja](https://github.com/vistaarjuneja))
- anka setup command & allow multiple setup pool [\#131](https://github.com/drone-runners/drone-runner-aws/pull/131) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(maint\) run go mod tidy, make sure it stays up to date [\#129](https://github.com/drone-runners/drone-runner-aws/pull/129) ([tphoney](https://github.com/tphoney))
- \(feat\) MacOS M1 Support  [\#128](https://github.com/drone-runners/drone-runner-aws/pull/128) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(maint\) add mac testing steps to release prep docs [\#126](https://github.com/drone-runners/drone-runner-aws/pull/126) ([tphoney](https://github.com/tphoney))

## [v1.0.0-rc.6](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.6) (2022-06-10)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.5...v1.0.0-rc.6)

**Implemented enhancements:**

- \[DRON-342\] Implement Drone Runner for Windows on GCP [\#122](https://github.com/drone-runners/drone-runner-aws/pull/122) ([ccaspanello](https://github.com/ccaspanello))
- \(DRON-324\) simplify setup for google [\#121](https://github.com/drone-runners/drone-runner-aws/pull/121) ([tphoney](https://github.com/tphoney))
- \(DRON-333\) add custom tags for AWS [\#118](https://github.com/drone-runners/drone-runner-aws/pull/118) ([tphoney](https://github.com/tphoney))
- \(DRON-284\) provide better OS support [\#117](https://github.com/drone-runners/drone-runner-aws/pull/117) ([tphoney](https://github.com/tphoney))
- Add support for amazon linux 2 ami hibernate [\#105](https://github.com/drone-runners/drone-runner-aws/pull/105) ([shubham149](https://github.com/shubham149))
- Add stage owner support in pool ownership api [\#96](https://github.com/drone-runners/drone-runner-aws/pull/96) ([shubham149](https://github.com/shubham149))

**Fixed bugs:**

- fixes issue where m1 doesn't have /usr/local/bin folder [\#116](https://github.com/drone-runners/drone-runner-aws/pull/116) ([eoinmcafee00](https://github.com/eoinmcafee00))

**Merged pull requests:**

- prep for v1.0.0-rc.6 [\#125](https://github.com/drone-runners/drone-runner-aws/pull/125) ([tphoney](https://github.com/tphoney))
- google move defaults to options [\#123](https://github.com/drone-runners/drone-runner-aws/pull/123) ([tphoney](https://github.com/tphoney))
- add windows testing on merge to master [\#120](https://github.com/drone-runners/drone-runner-aws/pull/120) ([tphoney](https://github.com/tphoney))

## [v1.0.0-rc.5](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.5) (2022-05-25)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.4...v1.0.0-rc.5)

**Implemented enhancements:**

- \(dron-298\) allow env vars for delegate command [\#111](https://github.com/drone-runners/drone-runner-aws/pull/111) ([tphoney](https://github.com/tphoney))
- \(feat\) allow custom cloud-inits contents and files for a pool [\#108](https://github.com/drone-runners/drone-runner-aws/pull/108) ([tphoney](https://github.com/tphoney))
- \(feat\) allow min/max pool size to be set from env [\#106](https://github.com/drone-runners/drone-runner-aws/pull/106) ([tphoney](https://github.com/tphoney))
- \(DRON-281\) daemon can start using env vars [\#104](https://github.com/drone-runners/drone-runner-aws/pull/104) ([tphoney](https://github.com/tphoney))
- adding acceptance test and env vars [\#103](https://github.com/drone-runners/drone-runner-aws/pull/103) ([tphoney](https://github.com/tphoney))
- \(feat\) upgrade lite-engine to v0.1.0 [\#101](https://github.com/drone-runners/drone-runner-aws/pull/101) ([tphoney](https://github.com/tphoney))
- \(feat\) - new provider for building on osx - Anka [\#99](https://github.com/drone-runners/drone-runner-aws/pull/99) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(feat\) adding setup command [\#97](https://github.com/drone-runners/drone-runner-aws/pull/97) ([tphoney](https://github.com/tphoney))
- \(feat\) - osx support with vmware fusion [\#92](https://github.com/drone-runners/drone-runner-aws/pull/92) ([eoinmcafee00](https://github.com/eoinmcafee00))
- Add support for VM hibernate [\#91](https://github.com/drone-runners/drone-runner-aws/pull/91) ([shubham149](https://github.com/shubham149))
- Add support for DRONE\_RUNNER\_VOLUMES in the Drone AWS runner. [\#88](https://github.com/drone-runners/drone-runner-aws/pull/88) ([vistaarjuneja](https://github.com/vistaarjuneja))
- \(feat\) - store certs per pipeline & instance state [\#87](https://github.com/drone-runners/drone-runner-aws/pull/87) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(feat\) - provide ability for single pool file for all cloud providers [\#86](https://github.com/drone-runners/drone-runner-aws/pull/86) ([eoinmcafee00](https://github.com/eoinmcafee00))
- daemon: features that docker runner supports [\#84](https://github.com/drone-runners/drone-runner-aws/pull/84) ([marko-gacesa](https://github.com/marko-gacesa))
- Implemented network mode for daemon command [\#81](https://github.com/drone-runners/drone-runner-aws/pull/81) ([marko-gacesa](https://github.com/marko-gacesa))
- Added support for private repos and custom entrypoint [\#79](https://github.com/drone-runners/drone-runner-aws/pull/79) ([marko-gacesa](https://github.com/marko-gacesa))
- \(feat\) gcp support [\#78](https://github.com/drone-runners/drone-runner-aws/pull/78) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(DRON-220\) add availability zones for aws [\#74](https://github.com/drone-runners/drone-runner-aws/pull/74) ([tphoney](https://github.com/tphoney))

**Fixed bugs:**

- \(fix\) fix the aws env vars [\#109](https://github.com/drone-runners/drone-runner-aws/pull/109) ([tphoney](https://github.com/tphoney))
- \(fixes\)  dron-285 - fixes issues with anka & adds purger to daemon [\#107](https://github.com/drone-runners/drone-runner-aws/pull/107) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(feat\) have a single instance db for exec / setup. [\#102](https://github.com/drone-runners/drone-runner-aws/pull/102) ([tphoney](https://github.com/tphoney))
- fixes build script & add label functionality for pipelines [\#95](https://github.com/drone-runners/drone-runner-aws/pull/95) ([eoinmcafee00](https://github.com/eoinmcafee00))
- fixes issue cause by the introduction of sqlite [\#93](https://github.com/drone-runners/drone-runner-aws/pull/93) ([eoinmcafee00](https://github.com/eoinmcafee00))
- \(DRON-255\) fix typo for reporting instance failures [\#90](https://github.com/drone-runners/drone-runner-aws/pull/90) ([tphoney](https://github.com/tphoney))
- Handle missing pool file [\#82](https://github.com/drone-runners/drone-runner-aws/pull/82) ([marko-gacesa](https://github.com/marko-gacesa))
- pool manager: handling cancelled context [\#80](https://github.com/drone-runners/drone-runner-aws/pull/80) ([marko-gacesa](https://github.com/marko-gacesa))
- \(fix\) readd aws keypair [\#77](https://github.com/drone-runners/drone-runner-aws/pull/77) ([tphoney](https://github.com/tphoney))

**Merged pull requests:**

- rc5\_prep2 [\#115](https://github.com/drone-runners/drone-runner-aws/pull/115) ([tphoney](https://github.com/tphoney))
- \(maint\) publish happens after all checks pass [\#113](https://github.com/drone-runners/drone-runner-aws/pull/113) ([tphoney](https://github.com/tphoney))
- Build mac binary [\#100](https://github.com/drone-runners/drone-runner-aws/pull/100) ([tphoney](https://github.com/tphoney))
- Add dynamic wait for vm hibernate [\#98](https://github.com/drone-runners/drone-runner-aws/pull/98) ([shubham149](https://github.com/shubham149))
- \(task\) refactor pool/driver struct [\#94](https://github.com/drone-runners/drone-runner-aws/pull/94) ([eoinmcafee00](https://github.com/eoinmcafee00))
- delegate command uses its own config [\#83](https://github.com/drone-runners/drone-runner-aws/pull/83) ([marko-gacesa](https://github.com/marko-gacesa))
- compiler unit tests [\#76](https://github.com/drone-runners/drone-runner-aws/pull/76) ([marko-gacesa](https://github.com/marko-gacesa))

## [v1.0.0-rc.4](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.4) (2022-02-10)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.3...v1.0.0-rc.4)

**Implemented enhancements:**

- Update lite-engine version for port binding support [\#73](https://github.com/drone-runners/drone-runner-aws/pull/73) ([shubham149](https://github.com/shubham149))
- Use AWS IAM role to create VMs [\#70](https://github.com/drone-runners/drone-runner-aws/pull/70) ([shubham149](https://github.com/shubham149))

**Fixed bugs:**

- \(dron-217\) Remove SSH [\#72](https://github.com/drone-runners/drone-runner-aws/pull/72) ([marko-gacesa](https://github.com/marko-gacesa))
- \(fix\) default to amd64 if no arch is given [\#71](https://github.com/drone-runners/drone-runner-aws/pull/71) ([tphoney](https://github.com/tphoney))

**Merged pull requests:**

- \(maint\) rc4 release prep [\#75](https://github.com/drone-runners/drone-runner-aws/pull/75) ([tphoney](https://github.com/tphoney))

## [v1.0.0-rc.3](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.3) (2022-02-01)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.2...v1.0.0-rc.3)

**Implemented enhancements:**

- Pick vm with oldest creation time during instance provisioning [\#69](https://github.com/drone-runners/drone-runner-aws/pull/69) ([shubham149](https://github.com/shubham149))
- extend multi architecture support [\#68](https://github.com/drone-runners/drone-runner-aws/pull/68) ([eoinmcafee00](https://github.com/eoinmcafee00))
- improved cert gen func [\#67](https://github.com/drone-runners/drone-runner-aws/pull/67) ([marko-gacesa](https://github.com/marko-gacesa))
- daemon: run commands through LE [\#55](https://github.com/drone-runners/drone-runner-aws/pull/55) ([marko-gacesa](https://github.com/marko-gacesa))

**Fixed bugs:**

- fix for no max pool size issue [\#66](https://github.com/drone-runners/drone-runner-aws/pull/66) ([marko-gacesa](https://github.com/marko-gacesa))

## [v1.0.0-rc.2](https://github.com/drone-runners/drone-runner-aws/tree/v1.0.0-rc.2) (2022-01-14)

[Full Changelog](https://github.com/drone-runners/drone-runner-aws/compare/v1.0.0-rc.1...v1.0.0-rc.2)

**Merged pull requests:**

- release-prep-for-1.0.0-rc.2 [\#65](https://github.com/drone-runners/drone-runner-aws/pull/65) ([marko-gacesa](https://github.com/marko-gacesa))
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
