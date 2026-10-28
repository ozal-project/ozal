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
| Binario y versiones anteriores | `/usr/local/lib/switchboard/` | root, 755 |
| Configuración del host | `/etc/switchboard/config.json` | root, 644 si solo contiene referencias |
| Archivos de credenciales | `/etc/switchboard/*.token` | root:switchboard, 640 |
| SQLite, Pages, runtime y ACME | `/var/lib/switchboard/` | switchboard, directorio 700 |
| Socket administrativo | `/run/switchboard/admin.sock` | switchboard, 600 |

El [instalador](download-install.md#linux-instalar-el-servicio-de-una-vez)
prepara el servicio. La [unidad de ejemplo](switchboard.service) documenta
sus permisos. No hagas que un usuario sin autoridad pueda reemplazar binario,
configuración o estado para conseguir acceso al reiniciar.

Con `runtime_file`, la consola guarda rutas, proyectos y la referencia de la
GitHub App en un archivo del servicio; listeners, TLS y autoridad permanecen
en la configuración del host. Inicialízalo una vez antes de arrancar:

```sh
sudo -u switchboard /usr/local/lib/switchboard/switchboard runtime init \
  -config /etc/switchboard/config.json
```

El destino debe ser nuevo. Después, un runtime ausente o inválido provoca
rechazo; no se recuperan silenciosamente políticas viejas del archivo raíz.
Inclúyelo en backups. Una recarga fallida conserva el snapshot válido anterior;
`config_sha256` en el estado autenticado identifica la configuración efectiva.
Modificar listeners, identidad del túnel o `runtime_file` requiere reinicio.

## Actualizar y revertir

`switchboard upgrade` se ejecuta desde el **binario nuevo**, verificado y
copiado a un directorio propiedad de root. Ejemplo: sustituye SHA y rutas por
valores comprobados; no ejecutes como root un archivo modificable por otra cuenta.

```sh
sudo /ruta/root-owned/switchboard-new upgrade -unit switchboard \
  -binary /usr/local/lib/switchboard/switchboard -sha256 SHA256_ESPERADO \
  -config /etc/switchboard/config.json \
  -db /var/lib/switchboard/state.db -user switchboard
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
switchboard db backup -db /var/lib/switchboard/state.db -out ./state.backup.db
switchboard db restore -from ./state.backup.db -to ./state.restored.db
switchboard doctor -config /etc/switchboard/config.json -db ./state.restored.db
switchboard migrate -db ./state.restored.db status
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
switchboard connector prepare -dir ./connector-identity -name homelab
# Host autorizado de la CA: solo recibe el CSR.
switchboard connector sign -csr ./connector.csr \
  -ca-dir /ruta/ca-existente -out ./connector.crt
# Devuelve el certificado público a su máquina; nunca transfieras la clave.
switchboard connector check -dir ./connector-identity -ca ./ca.crt
# Edge: enrolamiento con autoridad del socket local.
switchboard connector enroll -config /etc/switchboard/config.json \
  -name homelab -cert ./connector.crt
```

Configura `tunnel` en el edge y una ruta con `connector: "homelab"`. El
transporte actual permite loopback o una subred RFC1918/ULA explícita; no es
un listener público genérico. El conector valida nombre/certificado del edge
y solo sirve sus destinos fijos. Para las opciones exactas usa
`switchboard tunnel connect -h`; los contratos están en
[desarrollo](../engineering.md#tuneles).

El certificado emitido por este flujo dura 30 días. Planifica y prueba rotación
antes de vencer. La renovación del piloto mediante SSH/roles específicos no
es renovación genérica para cualquier conector. Si no hay metadatos de
vencimiento, la consola informa desconocido.

Para retirar: revisa apps afectadas y usa
`switchboard connector revoke -config /etc/switchboard/config.json -name homelab`
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
switchboard github connect -config /etc/switchboard/config.json \
  -app-id 123 -installation-id 456 -key file:/etc/switchboard/github-app-key
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

<a id="operacion-remota-con-sbctl"></a>

## Operación remota con sbctl

`sbctl` se ejecuta en la máquina del operador, habla por SSH y muestra un plan
antes de aplicar. Usa un archivo de targets privado —
[sbctl.example.json](../../sbctl.example.json)— con destinos, rutas, unidad,
cuenta, socket, lock y archivo del token de publicación limitado.

```sh
sbctl edge upgrade [-ref REF] [-apply]
sbctl edge config -base FILE [-cloudflare-ranges] [-apply]
sbctl site publish -site NAME -dir DIST [-host HOST] [-apply]
sbctl site cutover -host HOST -to IP -cloudflare-token FILE [-dir DIST] [-apply]
sbctl site cutover -host HOST -back EVIDENCE -cloudflare-token FILE [-apply]
```

Upgrade construye un ref comprometido y usa el procedimiento anterior. Config
requiere una base con `runtime_file` y sin rutas/proyectos/GitHub: conserva
la proyección operativa existente, valida como el servicio y verifica el digest
después del cambio. Si inicializa runtime, no reemplaza uno existente. Cambia
las rutas desde la consola a partir de entonces.

Publish empaqueta archivos regulares, publica por el admin privado y comprueba
el contenido cuando se pide `-host`; un fallo revierte al artefacto anterior.
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
