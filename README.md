# etlegacy-demosummaries

1. Download latest binary from [Releases](https://github.com/Vorschreibung/etlegacy-demosummaries/releases/latest).
2. Put it in some directory where you want the output log files to be, open a
   Terminal and run the binary with `-h` to see all the options.

### Example 1
```
$ ./etlegacy-demosummaries_linux_amd64 --kills-only-from Debugschreibung --multikills-only 3 \
    "/home/vor/.etlegacy/legacy/demos/2025-06/2025-06-13-100235-etl_braundorf.dm_84"

--- START - /home/vor/.etlegacy/legacy/demos/2025-06/2025-06-13-100235-etl_braundorf.dm_84 ---
00:29.60 ; Kill         ; Debugschreibung ; GRENADE_PINEAPPLE ; "BOT"Aimless ; Enemy
00:29.60 ; Kill         ; Debugschreibung ; GRENADE_PINEAPPLE ; [BOT]Fullmonty ; Enemy
00:29.60 ; Kill         ; Debugschreibung ; GRENADE_PINEAPPLE ; [BOT]Sean ; Enemy
---
00:35.60 ; Kill         ; Debugschreibung ; SATCHEL ; [BOT]Fullmonty ; Enemy
00:35.60 ; Kill         ; Debugschreibung ; SATCHEL ; [BOT]Backfire ; Enemy
00:35.60 ; Kill         ; Debugschreibung ; SATCHEL ; [BOT]Bullseye ; Enemy
00:35.60 ; Kill         ; Debugschreibung ; SATCHEL ; [BOT]NoAmmo ; Enemy
---
01:37.70 ; Kill         ; Debugschreibung ; STEN ; [BOT]Malin ; Enemy
01:40.35 ; Kill         ; Debugschreibung ; STEN ; [BOT]Hitnrun ; Enemy
01:42.20 ; Kill         ; Debugschreibung ; STEN ; [BOT]Bullseye ; Enemy
---  END  - /home/vor/.etlegacy/legacy/demos/2025-06/2025-06-13-100235-etl_braundorf.dm_84 ---
```

There will now also be a `log-2025-06-13-100235-etl_braundorf.txt` next to where my
`./etlegacy-demosummaries_linux_amd64` binary is.

### Example 2
```
$ ./etlegacy-demosummaries_linux_amd64 split-multikill --from -me /home/vor/workspace/etlegacy-demosummaries/BelPracSupply.dm_84

/home/vor/workspace/etlegacy-demosummaries/BelPracSupply_00_13_18_u_a_ipod_2kills.dm_84
```

There will now be the listed clips of multikills in the current working
directory.
