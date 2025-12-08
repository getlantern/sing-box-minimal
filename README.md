# sing-box-minimal

The goal of this repository it to maintain a minimal fork of sing-box for use in Lantern and other tools choosing to use the Lantern fork that has as few changes from upstream as possible in order to easily stay in sync with the sing-box mainline. This is as soft a fork as possible.

As with all Lantern forks, the goal is to always contribute changes to the upstream whenever possible and whenever they're accepted. 

If you're looking for additonal features Lantern has added to sing-box, such as additional protocols not supported in the sing-box mainline, see [Lantern Box](https://github.com/getlantern/sing-box-extensions/).

The `lantern-main` branch is our primary working branch. It will automatically be synced with `SagerNet/sing-box/main` on a weekly basis, but you can also trigger a manual sync by running the auto sync workflow.

## 

The universal proxy platform.

[![Packaging status](https://repology.org/badge/vertical-allrepos/sing-box.svg)](https://repology.org/project/sing-box/versions)

## Documentation

https://sing-box.sagernet.org

## License

```
Copyright (C) 2022 by nekohasekai <contact-sagernet@sekai.icu>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.

In addition, no derivative work may use the name or imply association
with this application without prior consent.
```

To sync with the latest sing-box mainline, simply run:

```
git fetch upstream
git merge upstream/main
git push origin lantern-main
```

To make updating other repos easier, you can then do, for example:

```
git tag -a v1.11.11-lantern -m "tagging latest"
git push --tags
```
