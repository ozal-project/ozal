# Administrar una instalación

Para instalar o usar la consola, sigue [instalación](download-install.md) y
[primera aplicación](first-application.md). Aquí se reúnen operación, backups,
conectores y publicación. Las funciones disponibles dependen de la versión
instalada; los pendientes de desarrollo están en [el tracker](../TO_IMPLEMENT.md).

## Servicio y propiedad del estado

El servicio Linux se ejecuta con una cuenta propia. El binario y la
configuración del host pertenecen a root; el estado al servicio.

| Recurso | Ruta recomendada | Propiedad/permisos |
|---|---|---|
| Binario y versiones anteriores | `/usr/local/lib/ozal/` | root, 755 |
| Configuración del host | `/etc/ozal/config.json` | root, 644 si solo contiene referencias |
| Archivos de credenciales | `/etc/ozal/*.token` | root:ozal, 640 |
| SQLite, Pages, runtime y ACME | `/var/lib/ozal/` | ozal, directorio 700 |
| Socket administrativo | `/run/ozal/admin.sock` | ozal, 600 |

El [instalador](download-install.md#linux-instalar-el-servicio-de-una-vez)
prepara el servicio. La [unidad de ejemplo](ozal.service) documenta
sus permisos. No hagas que un usuario sin autoridad pueda reemplazar binario,
configuración o estado para conseguir acceso al reiniciar.

Con `runtime_file`, la consola guarda rutas, proyectos y la referencia de la
GitHub App en un archivo del servicio; listeners, TLS y autoridad permanecen
en la configuración del host. Inicialízalo una vez antes de arrancar:

```sh
sudo -u ozal /usr/local/lib/ozal/ozal runtime init \
  -config /etc/ozal/config.json
```

El destino debe ser nuevo. Después, un runtime ausente o inválido provoca
rechazo; no se recuperan silenciosamente políticas viejas del archivo raíz.
Inclúyelo en backups. Una recarga fallida conserva el snapshot válido anterior;
`config_sha256` en el estado autenticado identifica la configuración efectiva.
Modificar listeners, identidad del túnel o `runtime_file` requiere reinicio.

## Migrar de Switchboard a Ozal

El nombre del producto y de los comandos pasa a ser **Ozal** (`ozal` y
`ozalctl`). Una instalación existente conserva su cuenta, unidad, configuración,
socket, SQLite, runtime, CA y estado TLS. No ejecutes el instalador de una
instalación nueva sobre ella ni muevas el estado para cambiar la marca.
El instalador rechaza el layout anterior para evitar crear dos servicios.

Usa el procedimiento de actualización de la sección siguiente con el binario
Ozal y los valores **reales anteriores** de `-unit`, `-binary`, `-config`,
`-db` y `-user`. Esos flags no dependen del nombre del producto. Conserva los
backups y la unidad anterior para revertir; cambiar sus nombres es otra operación.
Los agentes de renovación del piloto conservan sus rutas y unidades anteriores.

`OZAL_CONFIG` sustituye a `SWITCHBOARD_CONFIG` y `OZAL_ADMIN_TOKEN` a
`SWITCHBOARD_ADMIN_TOKEN`; las variables anteriores siguen aceptadas como
compatibilidad. El token nuevo, si está definido incluso vacío, tiene prioridad.
Sin `-config`, se busca la variable nueva, la anterior, `./config.json`,
`/etc/ozal/config.json` y por último `/etc/switchboard/config.json`.
`migrate` usa `ozal.db` por defecto, pero reutiliza `switchboard.db` si existe
y el nuevo no; en instalaciones usa siempre el `-db` real.

La consola prefiere **`ozal.localhost`**. Las passkeys existentes siguen
funcionando en `switchboard.localhost` o `localhost`, con el RP ID original.
Entra por ese nombre, genera un enlace para agregar otro navegador y ábrelo
en `ozal.localhost` para registrar una passkey nueva para la misma cuenta.
El cambio de nombre no permite usar una credencial de un RP ID en otro.
Las sesiones de consola en memoria terminan al reiniciar, como antes.

Los identificadores de protocolo también pasan a Ozal: cabeceras `X-Ozal-*`,
ALPN del túnel (`ozal-tunnel-spike/1`), ruta del probe (`/.well-known/ozal-probe/`)
y los dominios de las derivaciones criptográficas (cookie de login, callback de
Telegram, replay de deploy y cadena del audit log). **No es retrocompatible**:

- Edge y conector deben actualizarse juntos; con versiones mezcladas el túnel
  no negocia ALPN.
- Las cookies de login OIDC emitidas antes del cambio dejan de validar: los
  visitantes entran de nuevo.
- Un log de auditoría escrito antes del cambio **no verifica** con el binario
  nuevo (el dominio entra en cada hash). Archívalo con su binario anterior y
  empieza un log nuevo; no lo reescribas.
- Las cabeceras `X-Switchboard-Visitor-*` y sus variantes con `_` se siguen
  eliminando como entrada de cliente, pero ya no significan nada para Ozal.
- Se conservan: cookies `sb_*`, sujetos nativos `sb:`, migraciones SQL (con su
  checksum) y las variables, rutas y nombres de host anteriores listados arriba.

El código fuente y el módulo ya usan `github.com/ozal-project/ozal`; el sitio
público es `ozal.octaviosantacruz.com`. La publicación del sitio y su trust OIDC
se aceptaron por separado en `SITE-006`. Eso no actualiza el binario del edge
ni del conector y no implica que exista un nuevo tag o descarga Ozal.

## Actualizar y revertir

`ozal upgrade` se ejecuta desde el **binario nuevo**, verificado y
copiado a un directorio propiedad de root. Ejemplo: sustituye SHA y rutas por
valores comprobados; no ejecutes como root un archivo modificable por otra cuenta.

```sh
sudo /ruta/root-owned/ozal-new upgrade -unit ozal \
  -binary /usr/local/lib/ozal/ozal -sha256 SHA256_ESPERADO \
  -config /etc/ozal/config.json \
  -db /var/lib/ozal/state.db -user ozal
```

Valida, detiene la unidad, respalda/migra si hay migraciones, conserva el
binario anterior y espera servicio activo más respuesta de administración.
Ante fallo restaura binario y, si migró, base previa. Hay una interrupción
durante el cambio; no se promete alta disponibilidad. El hash lo verifica
contra el valor suministrado, no autentica por sí solo el canal de descarga.

| Salida | Resultado |
|---|---|
| 0 | Nueva versión activa, o ya estaba instalada |
| 2 | Rechazado antes de detener el servicio |
| 3 | Falló y la versión anterior volvió a responder |
| 4 | Se intentó restaurar la anterior, pero no responde: intervención necesaria |

Los backups no se borran automáticamente. Una versión anterior puede rechazar
un esquema nuevo; revertir solo el ejecutable puede ser insuficiente.

### Actualización automática

`ozal update` consulta la versión publicada más nueva. Con `-apply` instala
en **un host**, usando `ozal upgrade` y su vuelta atrás. Solo acepta un release cuyo `checksums.txt`
esté firmado (Ed25519) con la clave que el host fija en
`/etc/ozal/release.pub`; comprueba el hash del archivo antes de extraer el
binario y nunca instala una versión igual o más vieja que la instalada. Los
borradores no cuentan, y los prereleases solo con `-prerelease`.

```sh
sudo install -m 0644 release.pub /etc/ozal/release.pub
sudo /usr/local/lib/ozal/ozal update -public-key /etc/ozal/release.pub \
  -unit ozal -binary /usr/local/lib/ozal/ozal -config /etc/ozal/config.json \
  -db /var/lib/ozal/state.db -user ozal              # informa; -apply instala
sudo install -m 0644 docs/operations/ozal-update.service docs/operations/ozal-update.timer /etc/systemd/system/
sudo systemctl enable --now ozal-update.timer         # comprueba disponibilidad, cada noche
```

Un binario que no es un release (`ozal version` dice `dev`, compilado a
mano) **no se reemplaza solo**: el release más nuevo podría ser más viejo que
lo que corre. Pasa a releases una vez con `-apply -force` solo en un host
independiente. El timer de ejemplo **solo consulta**: no instala nada por sí
mismo. En el edge ALPN del túnel, actualízalo en coordinación con el conector;
no uses ahí el `-apply` de una ejecución por host, porque una mezcla de
versiones corta el túnel. `-status FILE` deja el resultado (`current`,
`available`, `updated`, `failed`, `unversioned`) en JSON legible por la cuenta
del servicio, sin claves ni tokens. La consola puede leer el estado autenticado
en `GET /_/api/updates/status` con la herramienta de operador `updates`; la API
solo informa, no inicia procesos. La razón mostrada se reduce a un mensaje
seguro; los detalles quedan en el host. Salida 3 de `ozal upgrade` confirma
reversión; salida 4 requiere intervención.

Quien publica: `ozal release keygen -key release.key` crea la clave e imprime
la pública. Guarda la privada como secreto `OZAL_RELEASE_KEY` del workflow
`release` y borra el archivo; sin ese secreto el workflow falla en vez de dejar
un release sin firma. El release sigue siendo un borrador hasta que alguien lo
publica, y solo entonces lo ven los edges. **Quien tenga esa clave puede
instalar código en todos los edges que la fijan**: rótala publicando la nueva
pública en los edges antes de firmar con ella.

### Actualización coordinada del edge y el conector

Para `REL-013/017`, trata ambos extremos como **una pareja de versiones**.
`ozalctl edge upgrade` actualiza solamente el edge: no instala ni revierte el
conector, no pausa su renovación y no comprueba aplicaciones. Su salida 0
prueba arranque y admin; todavía falta la aceptación del recorrido completo.
No encadenes dos actualizaciones independientes suponiendo compatibilidad ALPN.

El target puede incluir `connector` con su host SSH, cuenta de servicio, unidad
de usuario, ejecutable, staging y lock. Entonces `ozalctl edge upgrade` se
rechaza para ese target; usa `ozalctl edge pair-upgrade`. El comando construye
un release Linux/amd64 firmado indicado con `-ref TAG` y `-public-key FILE`,
toma ambos locks, verifica hashes instalados y subidos, guarda el ejecutable
anterior en cada host y una copia consistente
SQLite en el edge, detiene conector y edge, instala el mismo binario, inicia
edge y conector y comprueba el túnel mediante el socket local. El upgrade del
edge migra SQLite como su cuenta de servicio, pero deja la unidad detenida
hasta instalar el conector. Ante un fallo
después de iniciar el corte, intenta restaurar ambos ejecutables y la base del
edge; si no puede confirmar recuperación, devuelve intervención requerida.
El estado se informa por el socket local y queda disponible en
`GET /_/api/updates/status` para sesiones con la herramienta `updates`.
`GET /_/api/connectors/{name}/ready` es una sonda host-only usada por el
coordinador. El POST de progreso se tolera como 404 solo antes de que el
candidato instale esa ruta; si el edge ya volvió y el endpoint no existe, se
revierte el trabajo. Las fases finales se guardan al volver el edge.
Un 503 o cualquier otro error detiene el trabajo antes del corte o activa su
reversión. `ozalctl edge pair-upgrade` sin `-apply` solo descarga y verifica el
artefacto: no contacta los hosts. Antes de aplicar, corre:

```sh
ozalctl edge pair-preflight [-targets FILE] [-target NAME]
```

Este comando sí conecta por SSH y verifica las versiones/hashes actuales, las
unidades activas, usuario y grupo efectivos de systemd, acceso del servicio a
configuración y SQLite, socket del admin, espacio de staging y locks libres.
No crea archivos ni locks, no detiene servicios y requiere una terminal para
las consultas `sudo` de solo lectura en Edge. El resultado es una observación,
no una reserva: `pair-upgrade -apply` repite la inspección bajo ambos locks.
`edge.service_group` en el target permite fijar el grupo esperado; si se omite,
se descubre de la unidad y se usa al restaurar SQLite. No despliega ni migra
una VPS por sí solo.

Antes de la ventana, completa esta ficha con datos observados, sin valores de
secretos; conserva la evidencia operativa en un destino privado:

| Dato | Edge | Conector |
|---|---|---|
| Versión anterior | Commit/build y SHA-256 del ejecutable instalado | Commit/build y SHA-256 instalado |
| Candidato común | Ref comprometido, SO/arquitectura y SHA-256 del artefacto | Mismo ref, artefacto de su arquitectura y SHA-256 |
| Servicio real | Host, unidad system/user, cuenta, ejecutable, config, DB, socket | Host, unidad system/user, cuenta, ejecutable, argumentos y rutas fijas |
| Estado | Config raíz, runtime efectivo, SQLite, Pages, TLS y audit | Identidad local, CA pública, configuración y renovación |
| Recuperación | Pareja anterior conservada, checkpoint privado y destino nuevo de restore | Ejecutable anterior conservado y configuración anterior |

En el piloto hay unidades y agentes de renovación con nombres históricos.
No los renombres durante este corte. Identifica también cualquier proceso
`tunnel edge` separado: cada edge que reciba ese conector necesita una pareja
compatible. No deduzcas la topología de una reserva de puertos ni de un nombre
de servicio. Registra el `config_sha256` efectivo usando el socket autenticado;
con `runtime_file`, la raíz no describe por sí sola las rutas/publicadores.

#### Topología observada y checkpoint privado — 2026-10-29

Inventario anterior a la instalación del 2026-10-29: la lectura encontró tres
edges en la VPS y un conector en el homelab. Los binarios de la pareja en esta
tabla son históricos; el resultado de la instalación figura más abajo. Los
nombres históricos no describen una única función:

| Extremo | Unidad / host | Ejecutable y SHA-256 | Estado observado |
|---|---|---|---|
| Preview público | `switchboard-preview.service`, VPS | commit `da3d0e6`, `0ca4c91d…` | activo; HTTPS público 443; admin `127.0.0.1:9091`; DB/Pages/runtime/TLS en `/var/lib/switchboard-preview` |
| Tunnel edge (ALPN) | `switchboard-tunnel-edge.service`, VPS | `(devel)`, `b5cba887…` | activo; HTTP `127.0.0.1:18889`, admin `127.0.0.1:9092`, mTLS `10.8.0.1:9443`; DB/identidad en `/var/lib/switchboard-tunnel-edge` |
| Conector | `switchboard-tunnel-connector.service`, homelab | `9701fa4`, `b2761ed3…` | estaba **inactivo desde 2026-10-26 01:25 UTC** al inventariar; activo y reiniciado el 2026-10-29 (ventana) |
| Staging histórico | `switchboard-staging.service`, VPS (usuario) | `eb8b805`, `e7b973d8…` | activo sólo en loopback (8888/9090); no participa del par |
| Preview staging | `switchboard-preview-staging.service`, VPS | mismo preview | inactivo/static |

La ruta real es preview (443) → tunnel edge (`127.0.0.1:18889`) → conector
(`10.8.0.1:9443`). `ozalctl edge upgrade` sólo tiene un target para
`switchboard-preview`; **no actualiza el tunnel edge ni el conector**. No
encadenes la actualización sin preparar esas dos unidades y su vuelta.

El conector estaba inactivo al inventariar; su certificado
`CN=connector.homelab.internal` vence el 2026-11-21 y la CA del piloto el
2027-10-18. El timer de renovación sigue activo (próxima ejecución
2026-10-30 00:09 UTC) y no se pausó. El checkpoint privado del conector
(identidad, CA, CA de origin-pull, unidades, estado de renovación, binarios y
journal) se creó en el homelab y se restauró en un directorio descartable:
cadena, correspondencia clave/certificado, hashes, sintaxis de unidad y
ejecución del binario pasan. No se conectó la identidad de producción a ningún
edge.

El checkpoint privado del edge requiere root en la VPS; `sudo -n` no está
disponible. El procedimiento preparado crea un destino nuevo, toma un snapshot
online de SQLite, copia Pages contra las filas del snapshot, runtime,
TLS/ACME, identidad del túnel y tokens, y prueba la restauración en loopback
con el binario anterior. No detiene unidades ni toca timers. En modo
`--quiesced` no consulta los sockets admin (las unidades están detenidas por
contrato).

**Checkpoint completo del edge, 2026-10-29 (UTC).** Un primer intento terminó
`incomplete` porque el capturador sondeaba esos sockets; se corrigió
(`capture-edge.py` `db4a567e…`). El segundo produjo
`/var/backups/ozal/REL-013-017-20261029T172304Z` (root, 0700; no se copió
fuera del host): `manifest.json` `d78be43b…` con `status` complete,
`coherent` true, sin fallos, `served_matches_live`, `identity_key_match` y
`expected_match` true; `inventory.tsv` `1bfe6a6d…`, `SHA256SUMS` `112f4818…`.
`ROLLBACK.md` (`868116b9…`) está en el directorio índice de octavio. Interrupción
medida: conector 17:22:39→17:23:45 (66 s), tunnel edge 17:22:57→17:23:21 (24 s),
preview 17:23:00→17:23:18 (18 s). Tras recuperar, healthz del preview 200,
`config_sha256` del edge `4b3415c9…` y preview 200 por el túnel. Límites: sin
copia fuera del host, sin `audit`, sin login ni app real verificados, y el
registro no mostró al conector adjunto. No declara listo el corte real.

**Instalación de la pareja `8f20fbe`, 2026-10-29 (UTC).** Un único artefacto
para los tres roles: `14fde470bab6f666…` (31162239 bytes; `go build -trimpath
-buildvcs=false -ldflags "-X main.buildCommit=<commit corto>"`, el mismo que
construye `ozalctl`; con el hash completo del commit el binario sale distinto).
Antes de la ventana: `validate` de la config del tunnel edge con el binario
nuevo OK y ensayo de `migrate up -backup` sobre una copia del checkpoint
(008–015 aplicadas, copia borrada). Instalación: DB real migrada con el binario
nuevo como `switchboard-tunnel` (el binario se instala primero como
`switchboard.new` en `/usr/local/lib/switchboard-tunnel-edge/`, porque esa
cuenta no entra a `/home/octavio`), ejecutable anterior conservado como
`switchboard.bak-b5cba887`; preview con `ozalctl edge upgrade -ref 8f20fbe
-apply` (anterior en `switchboard.bak-0ca4c91d7277`); conector nuevo instalado
el 18:12:14 (anterior `switchboard.bak-b2761ed3`). El conector anterior
estuvo activo frente al edge nuevo hasta ese momento, contra lo indicado más
arriba. Resultado: los tres ejecutables `14fde470…`; healthz del preview 200;
preview y `sigea-api` (`/healthz`) 200 por el túnel; conector y timer activos.

Cambio de comportamiento: `GET /_/api/status` por TCP en el admin del tunnel
edge (9092) responde 503 `admin mutations are disabled until admin.token is
configured`; antes devolvía 200. El estado se lee por el socket admin
(`/run/switchboard-tunnel-edge/admin.sock`) o con token. Las sondas por TCP sin
token dejan de servir.

No verificado: login real, política permitida/denegada, ausencia de identidad
falsificada y cookie de sesión en el origen, publicación de prueba y su
rollback. Verificado por el socket admin del edge tras la actualización:
`config_sha256` `4b3415c9…` (igual que antes) y "Connector connected" para
`preview.octaviosantacruz.com` y `sigea-api.octaviosantacruz.com`. Sin copia
fuera del host ni audit. No declara aceptado el corte.

Interrupción medida de la instalación (journal, UTC): tunnel edge parado
18:09:55→18:11:35 (100 s); preview reiniciado por `ozalctl` en 18:11:11 (la
parada y el arranque caen en el mismo segundo); conector cambiado en 18:12:14
(mismo segundo). El contenido público sale por el túnel: estuvo sin servicio
desde 18:09:55 hasta que el conector se reenganchó, con cotas observadas de
200 por el túnel a las 18:12:23 (≈2 min 30 s como máximo). Tras la
actualización, `https://preview.octaviosantacruz.com/` responde 200 vía
Cloudflare y `/_/healthz` y `/_/api/status` dan 404 desde fuera.

Antes de la instalación, el esquema del tunnel edge tenía **ocho migraciones
pendientes** (008–015) respecto del candidato `8f20fbe`; estas se aplicaron
durante la instalación registrada arriba. La reversión a la versión anterior
exige restaurar el snapshot previo a una ruta nueva. Entre `da3d0e6` y
`8f20fbe` el preview no tenía migraciones pendientes. Ninguno de
los dos edges tiene `audit` configurado: no hay cadena/checkpoint durable que
archivar; si se habilita, archivá la cadena anterior con su binario por el
cambio de dominio del hash.

1. **Preparar sin cortar.** Ejecuta el gate del ref candidato, construye ambos
   artefactos y verifica sus hashes. Revisa `ozalctl edge upgrade -ref REF`
   sin `-apply`. Valida configuración, esquema, identidades y permisos con
   el candidato en un entorno privado; no imprimas configuración ni claves.
   Registra una línea base: túnel conectado, contenido de una app, visitante
   permitido y denegado, login, publicación y versión actualmente publicada.
2. **Comprobar recuperación antes de instalar.** Conserva los dos ejecutables
   anteriores y un backup privado de todos los recursos de la tabla. SQLite
   requiere snapshot consistente. Restaura en un destino nuevo, verifica con
   el binario anterior y prueba la pareja anterior en aislamiento. El backup
   automático de `upgrade` solo cubre DB cuando hay migraciones pendientes;
   no sustituye este checkpoint ni incluye Pages/runtime/identidades.
3. **Quiescer la pareja.** Bloquea nuevas publicaciones y cambios administrativos
   durante la ventana y coordina los timers/agentes de renovación que puedan
   reiniciar o rotar identidades. Detén primero el conector, después los edges
   afectados, y toma el checkpoint final consistente. Conserva su estado previo
   para reanudar solo lo que estaba activo. Hay una interrupción planificada;
   no anuncies disponibilidad continua durante el cambio de protocolo.
4. **Instalar ambos lados.** Usa los layouts existentes y los artefactos
   verificados. Si hay audit persistente, con el edge detenido prepara en la
   configuración un destino nuevo para la cadena Ozal y conserva la referencia
   anterior en el checkpoint; nunca abras la cadena vieja con el candidato.
   Para el edge administrado por `ozalctl`, aplica el plan revisado
   desde una terminal; el paso root valida y migra con la cuenta del servicio.
   Cambia el ejecutable del conector bajo la autoridad de su host, conservando
   el anterior y sin mover ni regenerar su clave. Inicia primero el edge y luego
   el conector. No arranques el conector anterior frente al edge candidato.
   El `upgrade` puede restaurar únicamente el edge si falla: en ese caso deja
   el conector detenido hasta elegir de nuevo una pareja completa.
5. **Aceptar o volver.** Confirma hashes instalados y estado efectivo, túnel
   conectado con la identidad enrolada, aplicación pública y protegida pasando
   por el túnel, login real y política permitida/denegada, ausencia de identidad
   falsificada/cookie de sesión en el origen y consola privada. Publica un
   artefacto de prueba en un sitio de prueba, verifica bytes/assets y prueba su
   rollback. Comprueba también los sitios ya publicados y el trust CI efectivo.
   Si no pasa cualquier criterio, detén la pareja y ejecuta la reversión; no
   debilites TLS, política ni enrolamiento para recuperar servicio.

**Reversión coordinada:** detén conector y edges candidatos; conserva su estado
para diagnóstico; restaura los dos ejecutables anteriores comprobando hashes.
Si cambió el esquema, usa la DB del checkpoint restaurada a una ruta nueva y
reconcilia revocaciones posteriores antes de servir. Restaura la configuración
y el runtime del mismo checkpoint, junto con el contenido/metadata de Pages
si cambió. No emparejes una DB antigua con un runtime de otra generación.
Revisa propietarios, permisos y rutas de la unidad, y valida con el binario
anterior. Arranca el edge anterior y después el conector anterior; repite todos
los criterios de aceptación y reanuda los timers/publicadores que corresponda.
Una reversión de artefacto de sitio no restaura DB, runtime ni protocolo.
Registra tiempo de interrupción, checkpoint elegido y escrituras descartadas;
sin checkpoint completo, no declares recuperación comprobada.

Secuencia concreta de vuelta (con la ventana autorizada y `<CHECKPOINT>` ya
verificado):

```sh
# Homelab: primero el conector.
systemctl --user stop switchboard-tunnel-connector.service
# VPS: después los edges; no dejes el conector anterior frente al candidato.
sudo systemctl stop switchboard-tunnel-edge.service switchboard-preview.service
# DB a rutas NUEVAS (nunca sobrescribir la del candidato).
sudo -u switchboard /usr/local/lib/switchboard-preview/switchboard db restore \
  -from <CHECKPOINT>/db/preview-state.db \
  -to /var/lib/switchboard-preview/state.rollback-<TS>.db
# El binario antiguo del tunnel edge no tiene db restore: usá el del preview
# o copiá el snapshot ya verificado y comprobá PRAGMA integrity_check.
sudo /usr/local/lib/switchboard-preview/switchboard db restore \
  -from <CHECKPOINT>/db/tunnel-state.db \
  -to /var/lib/switchboard-tunnel-edge/state.rollback-<TS>.db
# Restaurar runtime/pages/tls/tunnel desde <CHECKPOINT>, apuntar las unidades
# a la DB nueva con los mismos propietarios/permisos, y reinstalar los binarios
# anteriores desde <CHECKPOINT>/bin; verificar cada sha256 antes de arrancar.
sudo systemctl start switchboard-tunnel-edge.service switchboard-preview.service
systemctl --user start switchboard-tunnel-connector.service
```

Después repetí túnel, login real, app permitida/denegada, ausencia de identidad
falsificada/cookie en el origen y publicación. Reconciliá revocaciones
posteriores al checkpoint antes de devolver servicio.

La cadena de audit anterior se conserva intacta con su binario verificador;
el candidato escribe en un destino nuevo. En la vuelta conserva ambas cadenas
por separado y usa un destino compatible con el binario anterior. Reconciliar
revocaciones también aplica a usuarios, passkeys y conectores, no solo sesiones.
Las cookies OIDC antiguas requieren nuevo login tras el cambio; el reinicio
termina las sesiones de consola en memoria. No cambies RP IDs de passkeys.

El [ensayo local](../engineering.md#verificacion) ejercita ambos protocolos,
backup/restore y publicación con fixtures. No sustituye el ensayo de systemd
en VM limpia (`REL-012`) ni la aceptación de login y publicación en el servidor.

<a id="backup-y-recuperacion"></a>

## Backup y recuperación

Respalda **SQLite y sus archivos externos** mediante un mecanismo privado:

- Configuración del host y `runtime_file`.
- SQLite consistente; no copies un archivo activo ignorando su WAL.
- Contenido de `pages.dir`, o artefactos originales para republicar.
- Caché/estado TLS, cuenta ACME y credenciales de DNS para reemitir si hace falta.
- Identidad del túnel, bundles de confianza y claves CA administradas fuera del edge.
- Archivos y fuentes externas de secretos `file:`/`env:`, chats emparejados si se usan.
- Logs/checkpoints de auditoría y su clave pública fijada, con copia independiente.

El backup de DB usa `VACUUM INTO`, verifica integridad y publica en un archivo
nuevo con permisos privados. Prueba sobre un directorio descartable:

```sh
ozal db backup -db /var/lib/ozal/state.db -out ./state.backup.db
ozal db restore -from ./state.backup.db -to ./state.restored.db
ozal doctor -config /etc/ozal/config.json -db ./state.restored.db
ozal migrate -db ./state.restored.db status
```

En el piloto 2026-10-29 el binario del tunnel edge (`9701fa4`) no tiene
`db backup`: usá la API de backup de SQLite (Python) o el binario del preview
(`da3d0e6`) sobre la DB anterior; ambos producen un archivo autocontenido y se
probaron con el esquema previo. Verificá siempre la restauración en una ruta
nueva con el binario que va a leerla. El backup de la DB no incluye Pages,
runtime, TLS/ACME, identidad del túnel ni tokens: copiá esos recursos en el
mismo checkpoint y comprobá que las filas de `deployments`/`site_live` del
snapshot tengan sus directorios de Pages.

`restore` no sobrescribe destinos existentes e invalida sesiones por defecto.
`-keep-sessions` puede reactivar cookies viejas. Una copia antigua también puede
reactivar usuarios, passkeys, invitaciones o conectores revocados después:
reconcilia esas revocaciones antes de devolver servicio.

Para un host perdido: instala una versión verificada, restaura archivos y
propietarios con el servicio detenido, restaura DB a una ruta nueva, ejecuta
doctor y migraciones explícitas con backup, arranca y comprueba login, permisos,
sitios y túneles. Un integrity check no prueba que claves, DNS o conectores
estén disponibles. Mide pérdida de datos y tiempo de recuperación en ese ensayo.

## Incorporar un conector

Usa una CA existente y distribuye su certificado público por un canal
autenticado. Los placeholders son rutas elegidas por el operador.

```sh
# Máquina del conector: directorio nuevo, clave local y CSR público.
ozal connector prepare -dir ./connector-identity -name homelab
# Host autorizado de la CA: solo recibe el CSR.
ozal connector sign -csr ./connector.csr \
  -ca-dir /ruta/ca-existente -out ./connector.crt
# Devuelve el certificado público a su máquina; nunca transfieras la clave.
ozal connector check -dir ./connector-identity -ca ./ca.crt
# Edge: enrolamiento con autoridad del socket local.
ozal connector enroll -config /etc/ozal/config.json \
  -name homelab -cert ./connector.crt
```

Configura `tunnel` en el edge y una ruta con `connector: "homelab"`. El
transporte actual permite loopback o una subred RFC1918/ULA explícita; no es
un listener público genérico. El conector valida nombre/certificado del edge
y solo sirve sus destinos fijos. Para las opciones exactas usa
`ozal tunnel connect -h`; los contratos están en
[desarrollo](../engineering.md#tuneles).

El certificado emitido por este flujo dura 30 días. Planifica y prueba rotación
antes de vencer. La renovación del piloto mediante SSH/roles específicos no
es renovación genérica para cualquier conector. Si no hay metadatos de
vencimiento, la consola informa desconocido.

Para retirar: revisa apps afectadas y usa
`ozal connector revoke -config /etc/ozal/config.json -name homelab`
en el edge, después detén el conector en su host. La revalidación periódica
busca cerrar sesiones revocadas alrededor de cinco segundos; ante errores del
registro las existentes pueden permanecer y las conexiones nuevas se rechazan.
No elimines la CA compartida ni los datos de la app para revocar una identidad.

<a id="github-y-publicacion"></a>

## GitHub y publicación

Son tres funciones distintas: login OIDC de personas, descubrimiento de
repositorios con una GitHub App y autorización del workflow para publicar.
Una no concede automáticamente las otras.

La forma habitual es conectarla desde la consola, en **Integrations → GitHub →
Connect GitHub** o dentro del asistente (**Applications → Add application → A
website built from GitHub → Connect GitHub**); al volver de GitHub la consola
regresa a la página desde la que empezaste. Ozal crea en tu
cuenta (o en la organización que indiques) una App **privada** propia mediante
un manifest: `Metadata: read` y `Contents`, `Workflows` y `Pull requests` en
escritura, sin webhooks ni eventos. Apruébala en GitHub; GitHub vuelve a la
consola, Ozal guarda la clave de la App en `github-app-ID.pem` (modo 0600,
junto a la base de estado de `-db`) y te lleva a elegir los repositorios. Al
volver, la instalación se lee de GitHub con la clave de la App; nunca se toma
de la URL. Requiere la herramienta `publishing` y que Ozal arranque con `-db`.
La configuración guarda solo la referencia `file:`; el secreto OAuth y el de
webhook que GitHub devuelve se descartan.

Sin `-db`, o si prefieres crear la App a mano, guarda su clave como archivo
privado y configura únicamente la referencia desde el socket del host:

```sh
ozal github connect -config /etc/ozal/config.json \
  -app-id 123 -installation-id 456 -key file:/etc/ozal/github-app-key
```

Con GitHub conectado, el asistente lista los repositorios instalados, propone
la rama por defecto y el build (`npm ci && npm run build`, salida `dist`). La
revisión muestra el workflow generado. **Un solo guardado** crea la dirección,
deja ese repositorio, rama y workflow (`ozal-pages.yml`) como único publicador
del sitio y abre un PR que lo agrega; Ozal nunca lo fusiona. El último paso
indica el PR y espera la primera publicación antes de mostrar el DNS. Si falla
la confianza o el PR, la dirección queda guardada y el paso lo dice; termina en
Sites. Un sitio privado o un edge sin `pages.deploy` guarda la confianza pero no
abre PR: el workflow se escribe y revisa aparte. El asistente rechaza un nombre
de sitio que ya publique desde GitHub en vez de reemplazar su publicador.

**Integrations** muestra la conexión: App y cuenta, qué repositorios ve, qué
puede hacer, si el edge puede abrir PRs (`pages.deploy`) y qué sitios publican
desde GitHub. Si la instalación cubre **todos** los repositorios de la cuenta
lo advierte y enlaza a su página de GitHub para elegir sólo los que publican
sitios: Ozal pide escritura a un repositorio por vez, pero la clave de la App
alcanzaría a todos. La misma página explica que el login sigue siendo de Ozal
(passkeys u OIDC configurado): conectar GitHub o un proveedor DNS nunca permite
entrar con esa cuenta. Sin `pages.deploy` el asistente termina indicando que el
workflow se agrega aparte, en vez de esperar una publicación.

Desconectar sigue siendo una operación del host (`ozal github disconnect`);
borra la App en GitHub si ya no la usas y elimina su archivo de clave.

Para un sitio que ya existe, en Sites elige quién puede publicar: repo, sitio, proyecto, ref completa y
workflow. El servidor vuelve a comprobar acceso e IDs de repo/propietario.
El build corre en CI y el edge recibe el artefacto terminado; no compila código
del repositorio. Una vez guardado el publicador, **Prepare publishing workflow**
permite elegir el directorio de build, su salida y un hostname público del
proyecto. **Preview workflow** muestra el archivo sin escribir en GitHub;
**Review pull request** pide confirmar la rama y el archivo antes de crearlos.
La App necesita además `Contents: write`, `Workflows: write` y
`Pull requests: write`, solo en los repositorios elegidos. El token usado para
proponer el PR queda limitado al repositorio seleccionado; explorar sigue
pidiendo únicamente metadatos.

La plantilla inicial usa `npm ci` y `npm run build`; revisa el PR para otros
builds. Soporta subdirectorios y ramas con `/`, pero requiere una ref de rama,
un hostname público del proyecto y audiencia OIDC igual al origen HTTPS de
deploy. Los sitios protegidos y las audiencias personalizadas requieren un
workflow y una aceptación revisados por separado; no cambies su política para
usar esta plantilla. El job de build no puede pedir OIDC; el de deploy no
ejecuta código del repositorio. El checker/publicador y las acciones están
fijados por commit. La aceptación pública restaura el último build aceptado
en caché si existe; sin caché, el operador debe reconciliar la versión viva
y restaurar una versión retenida desde Sites.

Ozal agrega un archivo nuevo en una rama nueva, abre el PR y devuelve su
enlace; no reemplaza workflows ni ramas existentes, no hace merge y no
declara una publicación aceptada. Crear la rama puede activar otros workflows
de push del repositorio. Si la respuesta se interrumpe o falla después de una
escritura, inspecciona la rama `ozal/publish-…` y sus PRs antes de repetir:
la rama se conserva para reconciliar y no se fuerza ni borra automáticamente.
Fusionar el PR habilita la publicación en la rama elegida; verifica el primer
build, la política OIDC activa, la aceptación y el rollback reales.

El endpoint de deploy tiene hostname HTTPS dedicado y alcance reducido.
El workflow pide OIDC con la audiencia del edge, y deben coincidir repo,
propietario, ref, workflow y evento autorizados. Usa acciones fijadas y
`id-token: write` solo donde se despliega. Usa `ozalctl site check|pack|deploy|accept` (Go nativo) y consulta
[el contrato de publicación](../engineering.md#publicacion).
Los secretos de build no deben terminar en `dist`; los de runtime siguen en
el host de la aplicación.

Desconectar el publicador elimina confianza futura, conserva contenido y
rutas; una subida ya autenticada puede terminar con su snapshot capturado.
`github disconnect` elimina exploración API, **no** reglas OIDC guardadas;
retira los publicadores si quieres detener CI. Desinstalar la App tampoco
revoca esas reglas del edge.

<a id="operacion-remota-con-ozalctl"></a>

## Operación remota con ozalctl

`ozalctl` se ejecuta en la máquina del operador, habla por SSH y muestra un plan
antes de aplicar. Usa un archivo de targets privado —
[ozalctl.example.json](../../ozalctl.example.json)— con destinos, rutas, unidad,
cuenta, socket, lock y archivo del token de publicación limitado.

```sh
go build -o ./ozalctl ./cmd/ozalctl  # binario separado en la máquina operadora
ozalctl edge upgrade [-ref REF] [-apply]
ozalctl edge pair-upgrade -ref vX.Y.Z -public-key FILE [-force] [-apply]
ozalctl edge config -base FILE [-cloudflare-ranges] [-apply]
ozalctl site publish -site NAME -dir DIST [-host HOST] [-apply]
ozalctl app alias -from HOST -to HOST [-manual-dns] [-wait 90s] [-apply]
ozalctl api [-d JSON|@FILE|@-] METHOD /_/api/PATH
ozalctl project trust -project NAME -repository-id ID -owner-id ID -workflow-ref WORKFLOW [-ref refs/heads/main] [-apply]
ozalctl project rename -project NAME -to NUEVO [-apply]
ozalctl site cutover -host HOST -to IP -cloudflare-token FILE [-dir DIST] [-apply]
ozalctl site cutover -host HOST -back EVIDENCE -cloudflare-token FILE [-apply]
```

### Operar sin `sudo`: grupo del socket

Por defecto `app alias` y `project trust` corren en el edge como la cuenta de
servicio (`sudo -u`), así que piden tu contraseña y una terminal. Con
`admin.socket_group` el socket admin queda en modo `0660` para un grupo
dedicado: tu usuario SSH, si pertenece al grupo, abre el socket y los
comandos corren sin `sudo` ni TTY (sirven también desde scripts). La
auditoría registra `local:<usuario>` con el uid que informa el kernel; la
cuenta de servicio sigue figurando como `local`. Pertenecer al grupo equivale
a la autoridad del host (enrolar conectores, recuperar administradores):
dáselo sólo a quien ya tendría `sudo` en el edge.

```sh
sudo groupadd --system ozal-admin
sudo usermod -aG ozal-admin deploy        # tu usuario SSH; vuelve a iniciar sesión
sudo systemctl edit ozal                  # drop-in:
#   [Service]
#   SupplementaryGroups=ozal-admin
#   RuntimeDirectoryMode=0711
# config: "admin": {"socket": "/run/ozal/admin.sock", "socket_group": "ozal-admin"}
sudo systemctl restart ozal
```

`RuntimeDirectoryMode=0711` deja recorrer `/run/ozal` sin listarlo; el socket
mismo sigue cerrado a quien no está en el grupo. El servicio no arranca si el
grupo no existe, si su cuenta no pertenece a él o si el directorio del socket
no es recorrible: nunca queda un socket que parece compartido y no lo está.
En el archivo de targets agrega `"socket_group": "ozal-admin"` al `edge`;
si tu usuario todavía no puede abrir el socket, `ozalctl` se detiene antes de
ejecutar nada e indica el `usermod`. Instalar binarios, config y unidades
(`edge upgrade`, `edge config`, `pair-upgrade`) sigue necesitando root.

### La API como segundo camino: `ozalctl api`

Todo lo que expone la API admin (`/_/api/...`: zonas, aplicaciones, personas,
grupos, sitios, proyectos, conectores, política) se puede usar desde tu máquina
con `ozalctl api`, además del CLI y la consola. Necesita `socket_group` en el
target: el socket del edge se reenvía por SSH a un socket local dentro de un
directorio `0700` (nunca a un puerto TCP, que cualquier usuario local podría
usar) y la petición la hace tu usuario SSH, sin `sudo` ni token en tu máquina.

```sh
ozalctl api GET /_/api/zones
ozalctl api -d '{"name":"example.org","expected":"<config_sha256>"}' POST /_/api/zones
ozalctl api POST /_/api/zones/example.org/check
ozalctl api -d @people.json PUT /_/api/people/ana@example.org/groups
```

Sólo acepta rutas `/_/api/`, cuerpos JSON de hasta 1 MB y los métodos GET,
POST, PUT, PATCH y DELETE. Imprime la respuesta (JSON indentado); un HTTP 4xx o
5xx termina con estado 1 después de mostrarla. Antes de enviar nada comprueba
que el edge responde por el socket: si tu usuario no está en el grupo, se detiene
sin enviar la petición. Un error de red después de enviar deja el resultado
indeterminado: consulta el estado antes de repetir un cambio.

Upgrade construye un ref comprometido y usa el procedimiento anterior. Config
requiere una base con `runtime_file` y sin rutas/proyectos/GitHub: conserva
la proyección operativa existente, valida como el servicio y verifica el digest
después del cambio. Si inicializa runtime, no reemplaza uno existente. Cambia
las rutas desde la consola a partir de entonces.

Publish empaqueta archivos regulares, publica por el admin privado y comprueba
el contenido cuando se pide `-host`; un fallo revierte al artefacto anterior.

`project trust` migra el publicador GitHub de un proyecto existente cuando cambia
el repositorio o su organización. Verifica los IDs numéricos en GitHub y fija la
ref completa y el claim `owner/repo/.github/workflows/site.yml@refs/heads/main`.
Por defecto muestra el cambio; `-apply` conserva los metadatos anteriores en un
archivo privado `project-trust-NAME-FECHA.json` junto a la DB del servicio.
El archivo contiene IDs/ref/workflow para volver a configurar la confianza,
sin tokens, claves ni una copia completa del runtime. No se sobrescribe.
El edge compara el digest revisado, guarda por su API y confirma la confianza
activa; no necesita SIGHUP ni ediciones externas del runtime. Conserva sitios,
rutas y otros publicadores. Requiere exactamente un publicador GitHub previo;
si hay varios, revisa su configuración explícitamente. Esta autoridad pertenece
al socket del host, nunca a un token web o de publicación. Un error de red puede
dejar el resultado indeterminado: vuelve a consultar el plan antes de reintentar;
para revertir usa los valores `before` del backup en un nuevo comando revisado.
Como `app alias`, requiere primero un `edge upgrade` con un binario que incluya
el subcomando; la guardia se niega ante binarios anteriores sin arrancarlos.

`app alias` da a una aplicación pública existente (sitio estático o ruta a un
upstream) un segundo hostname sin tocar el primero. Rechaza aplicaciones con
política de acceso y rutas de redirect: el alias conserva exactamente la ruta
pública del origen. `ozalctl` ejecuta `ozal app alias` en el edge contra el socket admin
local (como la cuenta de servicio con `sudo`, o como tu usuario con
`socket_group`), así que no hay token nuevo en tu máquina. El edge guarda la ruta con el digest revisado, crea
el registro A proxied con su propia credencial (`dns.cloudflare.token`, un token
de Zone DNS Edit; puede ser el archivo que ya usa `tls.dns01`) y espera a que
`dns`, `certificate` y `reach` estén en `ok`. Nunca reemplaza un registro
existente: si ya hay uno, espera a que coincida. Sin `dns.cloudflare` se niega
antes de cambiar nada; con `-manual-dns` guarda la ruta y muestra el registro
que falta. Se repite sin riesgo. Si el binario del edge es anterior, el comando
se detiene antes de ejecutar nada y pide `edge upgrade`.

Cloudflare es opcional y tiene dos operaciones explícitas, separadas de DNS:

```sh
ozalctl cloudflare origin-pull -host NEW.example.com -like OLD.example.com -token-file TOKEN_FILE
ozalctl cloudflare deploy-rule -host deploy.example.com -token-file TOKEN_FILE
# Revisar el plan; agregar -apply para escribir y comprobar por lectura.
```

El archivo de token debe ser privado; los permisos de Origin Pulls y de
Configuration Rules son distintos de DNS Edit. `origin-pull` copia solamente la
asociación habilitada de un certificado existente en la misma zona: no exporta
claves, crea certificados ni sustituye asociaciones distintas. `deploy-rule`
ajusta sólo la regla gestionada del hostname exacto; no reemplaza el ruleset,
no altera reglas ajenas y se niega ante alcance ambiguo o parámetros adicionales.
Antes de escribir guarda metadatos 0600 en `~/.local/state/ozalctl/` (o
`-backup NEW_FILE`), vuelve a leer y verifica el resultado. Un fallo después de
escribir requiere consultar de nuevo al proveedor antes de repetir o revertir;
el backup no contiene credenciales ni toda la configuración de la zona.
Las escrituras se probaron contra una API local. Los planes nativos también
confirmaron por lectura el Origin Pulls del hostname Ozal y la regla de deploy
existentes; no se hicieron cambios en Cloudflare.

Cutover es una integración **actualmente específica de Cloudflare**, con
cambios de DNS y evidencia para restaurar. Un dominio asociado a Pages puede
requerir retirada manual allí. Consulta el plan y conserva evidencia; rollback
de contenido no revierte automáticamente DNS ni bases de datos.

## Ante un fallo

Comprueba por separado proceso (`/_/healthz`), configuración efectiva, conexión
del conector, funcionamiento de la app, DNS y HTTPS. Que responda el proceso
no prueba el recorrido completo. Para probar límites o recuperación, usa una
instancia descartable; no provoques disco lleno o caída de red en un host
compartido. Los comandos de desarrollo están en [ingeniería](../engineering.md#verificacion).
