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

El repositorio remoto, módulo Go y URL pública conservan sus destinos actuales
hasta confirmar dominio y organización. El renombrado local no registra un
dominio, publica una versión ni modifica el trust OIDC del edge. Un traslado
de repositorio necesita actualizar ese trust explícitamente antes de publicar.

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

El descubrimiento actual usa una App **propia** con `Metadata: read`, instalada
solo en los repositorios elegidos. Guarda su clave como archivo privado y
configura únicamente la referencia desde el socket del host:

```sh
ozal github connect -config /etc/ozal/config.json \
  -app-id 123 -installation-id 456 -key file:/etc/ozal/github-app-key
```

En Sites, elige quién puede publicar: repo, sitio, proyecto, ref completa y
workflow. El servidor vuelve a comprobar acceso e IDs de repo/propietario.
El build corre en CI y el edge recibe el artefacto terminado; no compila código
del repositorio. Una futura generación de PRs/workflows requiere permisos y
aceptación adicionales; no es parte del adaptador de metadatos.

El endpoint de deploy tiene hostname HTTPS dedicado y alcance reducido.
El workflow pide OIDC con la audiencia del edge, y deben coincidir repo,
propietario, ref, workflow y evento autorizados. Usa acciones fijadas y
`id-token: write` solo donde se despliega. Reutiliza
[scripts/pages](../../scripts/pages) y consulta
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
ozalctl edge upgrade [-ref REF] [-apply]
ozalctl edge config -base FILE [-cloudflare-ranges] [-apply]
ozalctl site publish -site NAME -dir DIST [-host HOST] [-apply]
ozalctl app alias -from HOST -to HOST [-manual-dns] [-wait 90s] [-apply]
ozalctl project trust -project NAME -repository-id ID -owner-id ID -workflow-ref WORKFLOW [-ref refs/heads/main] [-apply]
ozalctl site cutover -host HOST -to IP -cloudflare-token FILE [-dir DIST] [-apply]
ozalctl site cutover -host HOST -back EVIDENCE -cloudflare-token FILE [-apply]
```

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

`app alias` da a un sitio público existente un segundo hostname sin tocar el
primero. `ozalctl` ejecuta `ozal app alias` en el edge como su cuenta de
servicio (`sudo`, con tu terminal), así que usa el socket admin local y no hay
token nuevo en tu máquina. El edge guarda la ruta con el digest revisado, crea
el registro A proxied con su propia credencial (`dns.cloudflare.token`, un token
de Zone DNS Edit; puede ser el archivo que ya usa `tls.dns01`) y espera a que
`dns`, `certificate` y `reach` estén en `ok`. Nunca reemplaza un registro
existente: si ya hay uno, espera a que coincida. Sin `dns.cloudflare` se niega
antes de cambiar nada; con `-manual-dns` guarda la ruta y muestra el registro
que falta. Se repite sin riesgo. Si el binario del edge es anterior, el comando
se detiene antes de ejecutar nada y pide `edge upgrade`.

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
