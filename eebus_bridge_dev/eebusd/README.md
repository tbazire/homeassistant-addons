# eebus-scanner

Client EEBUS générique en Go, basé sur la librairie
[eebus-go](https://github.com/enbility/eebus-go). Il s'annonce en mDNS en tant
que **CEM** (Customer Energy Manager), s'apparie avec un équipement EEBUS
distant (par SKI), puis scanne et affiche toutes les données qu'il expose :

- métadonnées constructeur (`DeviceClassification`)
- paramètres de configuration (`DeviceConfiguration`)
- mesures électriques : puissance, énergie, courant, tension, fréquence
  (`Measurement`)
- état / diagnostic (`DeviceDiagnosis`)
- données typées par use case : **MGCP** (point de connexion), **MPC**
  (consommation appareil), **VABD** (batterie), **VAPD** (photovoltaïque)

C'est le remplaçant robuste de l'ancien script Python `eebus_vr920V2.py` qui
réimplémentait la pile SHIP/SPINE à la main.

## Pré-requis

- Go ≥ 1.24 (le `toolchain` directive du `go.mod` télécharge automatiquement
  la bonne version si ta machine a une version plus ancienne, à condition
  d'avoir accès à `proxy.golang.org`)
- Accès réseau au `proxy.golang.org` (pour résoudre les pseudo-versions
  `ship-go` / `spine-go` de mai 2026)
- mDNS fonctionnel sur la machine (Avahi sous Linux : service `avahi-daemon`)
- Le port UDP 5353 (mDNS) et le port TCP choisi (`-port`, défaut 4711)
  accessibles dans les deux sens sur le firewall

## Build

```sh
cd /home/tbazire/ZCodeProject/eebus_projet/eebus-scanner
go build -o eebus-scanner ./cmd/scanner
```

## Usage rapide

### 1. Découvrir les équipements EEBUS du réseau

```sh
./eebus-scanner -loglevel debug
```

Au bout de quelques secondes, la section `=== mDNS discovery ===` liste les
services visibles avec leur `ski`, `shipID`, `brand`, `model` :

```
=== mDNS discovery: 1 service(s) visible ===
  [0] ski=17953242bb44e3387401c1582be12dd8996e2bfc shipID=... brand=Saunier Duval model=SR940 type=Gateway
```

Note le `ski` de l'équipement cible.

### 2. S'apparier et scanner

```sh
./eebus-scanner -remoteski 17953242bb44e3387401c1582be12dd8996e2bfc -autoaccept -loglevel info
```

Étapes observées dans la console :

```
INFO  Local SKI:bb27...
INFO  SHIP QR code: SHIP;SKI:bb27...;ENDSHIP;
INFO  registering remote SKI 17953... for pairing
INFO  service started on port 4711 (CEM)
INFO  >>> remote service CONNECTED: ServiceIdentity{SKI:17953...}
INFO  === scanning remote device SKI=17953... ===
INFO  pairing state [...]: initiated
INFO  pairing state [...]: inProgress
INFO  pairing state [...]: trusted
INFO  pairing state [...]: completed
INFO  pairing COMPLETED with 17953242...
INFO  [MPC] Power = 17 W  (ski=17953... entity=3)
```

### 3. Sortie JSON (pour intégration future : MQTT, HA, etc.)

```sh
./eebus-scanner -remoteski ... -autoaccept -json
```

Chaque mesure est émise sur stdout en NDJSON :

```json
{"time":"2026-07-19T10:51:37Z","entity":"3","id":"0.0","type":"power","commodity":"electricity","scope":"ACPowerTotal","unit":"W","value":17}
```

## Flags

| Flag          | Défaut         | Rôle |
|---------------|----------------|------|
| `-port`       | `4711`         | Port TCP du serveur websocket SHIP local |
| `-certpath`   | *(vide)*       | Certificat PEM. Si vide, auto-généré et persisté dans `-certdir` |
| `-keypath`    | *(vide)*       | Clé privée PEM. Idem |
| `-certdir`    | `./certs`      | Répertoire de persistance : cert/key générés + `ringbuffer.json` |
| `-brand`      | `EEBusScanner` | Marque annoncée en mDNS |
| `-model`      | `Scanner-1`    | Modèle annoncé |
| `-serial`     | `scanner-0001` | Numéro de série annoncé |
| `-vendor`     | `SCNR`         | Code vendor (EEBUS) |
| `-heartbeat`  | `4s`           | Timeout heartbeat SHIP |
| `-remoteski`  | *(vide)*       | SKI (40 hex) du distant à appareiller |
| `-secret`     | *(vide)*       | Secret SHIP Pairing (hex, 16 octets) → active `PairingModeListener` |
| `-autoaccept` | `false`        | Trust automatique côté scanner (utile pour test) |
| `-loglevel`   | `info`         | `trace` / `debug` / `info` / `warn` / `error` |
| `-json`       | `false`        | Mesures en JSON lines au lieu de tableaux |
| `-list`       | `false`        | Lister les services découverts |

## Comment ça marche (architecture)

```
cmd/scanner/main.go      entry point : flags, lifecycle, gestion signaux
internal/app.go          orchestrateur : Service eebus + handlers SHIP
internal/config.go       flags + génération/persistance du certificat EC P-256
internal/logger.go       logger à niveaux (implémente logging.LoggingInterface)
internal/ringbuffer.go   persistance du SHIP Pairing ring buffer (JSON)
internal/scanner/
  scanner.go             scan générique via features/client (Measurement, ...)
  usecases.go            use cases typés read-only (MGCP, MPC, VABD, VAPD)
  log.go                 logger interne du package scanner
```

Deux mécanismes de lecture coexistent :

1. **Use cases typés** (`usecases.go`) : filtrent les mesures par scope/role
   EEBUS et produisent un output sémantique (`[MPC] Power = 17 W`). Ne
   fonctionnent que si l'entité distante a le type attendu (ex. MGCP → entité
   `GridConnectionPointOfPremises` ou CEM).
2. **Scan générique** (`scanner.go`) : via `features/client` (Measurement,
   DeviceClassification, DeviceConfiguration, DeviceDiagnosis), énumère tout
   ce que le distant expose, indépendamment de son type d'entité. C'est le
   filet de sécurité pour les équipements non standards.

## Notes importantes

- **Le certificat local est persistant** : une fois généré dans `-certdir`, il
  est réutilisé aux redémarrages. C'est critique, car le SKI dérive de la clé
  publique : le perdre force à ré-appairer tous les équipements. Ne supprime
  pas `certs/scanner.crt` et `certs/scanner.key`.
- **Le pairing nécessite souvent une action côté équipement** : touche
  physique, validation web, etc. `-autoaccept` ne fait qu'accepter le trust
  côté scanner, pas côté distant.
- **Le scan générique se déclenche tôt** après `RemoteServiceConnected`. Les
  entités complexes (cellules de mesure) sont découvertes *ensuite* via
  `NodeManagementDetailedDiscoveryData` ; les use cases typés, eux, attendent
  l'événement SPINE correspondant et matchent quand l'entité apparaît — c'est
  pour ça que tu vois `[MPC] Power = ...` même si le scan générique initial a
  loggué "no Measurement server feature".

## Troubleshooting

| Symptôme | Cause probable / action |
|----------|--------------------------|
| `address already in use` | Un process écoute déjà le port (`-port`). Change de port ou tue l'ancien. |
| Aucun service découvert | Vérifie `avahi-daemon` (Linux), le firewall (UDP 5353), que l'équipement est sur le même réseau L2/Broadcast. |
| `pairing state [...]: remoteDeniedTrust` | Le distant a refusé le trust. Relance en validant le trust côté équipement (QR, touche, etc.). |
| `panic: nil pointer` au `SetAutoAccept` | (corrigé) — `SetAutoAccept` doit être appelé après `service.Setup()`. |
| Le pairing reste `inProgress` | Souvent un souci de certificat (recrée le dossier `certs`) ou de version SHIP incompatible. Active `-loglevel trace`. |

## Hors périmètre (V1)

- Pas d'envoi de limites (LPC/LPP) ni de contrôle : lecture seule.
- Pas d'intégration MQTT/Home Assistant : sortie stdout uniquement
  (humain ou JSON lines). S'ajoutera dans une V2 une fois les données validées.
