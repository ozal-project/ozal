# Primera aplicación y web existente

Implementación local, 26/10/2026: `UI-006`, `PAGES-009`, `ID-013/014` y
`UI-007`. La consola sigue siendo privada y self-hosted. No hay cuenta SaaS.
La aceptación en otro equipo y una migración pública siguen siendo pruebas
externas; no se deducen de un build correcto.

## Probar con una aplicación que ya corre

Con un binario construido de este checkout:

```sh
switchboard setup -dir ./my-edge -target http://127.0.0.1:3000
```

Crea una instalación **nueva**: configuración, token privado referenciado por
archivo, SQLite migrado, directorio de Pages y `START-HERE.md`. Se niega a
sobrescribir un directorio existente. El listener público de esta prueba está
en `127.0.0.1:8888`; la ruta `localhost:8888` exige passkey y grupo `members`.
No instala un servicio ni altera DNS o una web que ya esté funcionando.

El comando imprime las rutas y los comandos exactos de arranque, invitación y
login del operador. Usa esas rutas; no copies credenciales a un chat.

```sh
switchboard -config /ruta/my-edge/config.json -db /ruta/my-edge/state.db
# En otra terminal del mismo host:
switchboard console enrol -config /ruta/my-edge/config.json
switchboard user invite -config /ruta/my-edge/config.json \
  -email ana@example.com -group members -return-to http://localhost:8888/
```

El primer enlace abre una sesión administrativa privada; el segundo es una
invitación de visitante. Son credenciales diferentes. Envía la invitación a
su destinataria por un canal confiable. Con un segundo perfil del navegador,
prueba que una persona sin `members` recibe una negativa. Que haya login no
implica que esa persona pueda administrar Switchboard.

Para un sitio estático, usa `setup -dir ./my-site -site portfolio`. La
configuración ya tiene persistencia y Pages. La primera publicación aún falta:
sube un tar.gz del contenido de `dist` en **Applications → Publish** (o al agregarla con **Add website**),
o usa `site deploy` con el token mediante su referencia a archivo. La web del
proyecto se construye fuera del edge.

En Windows no hay socket Unix administrativo; las sesiones del operador
emitidas por el host requieren Linux/macOS. Para la consola local Windows se
conserva el token por archivo y la recuperación offline con el proceso detenido.
La instalación de servicio descrita en este repositorio es Linux/systemd.

## Primer administrador y acceso por persona

El host abre la puerta una sola vez; después cada administrador entra con su
propio passkey y no hace falta ningún comando en el host.

```sh
sudo switchboard console enrol            # instalación con el servicio; por SSH imprime también el `ssh -N -L` para abrir el enlace
SWITCHBOARD_CONFIG=/ruta/config.json switchboard console enrol   # config fuera de /etc/switchboard
switchboard console enrol -config /ruta/my-edge/config.json   # arranque manual
```

Imprime un enlace que sirve una vez durante diez minutos. Ábrelo en el
navegador que tiene el túnel; pide nombre completo y correo (no se envía
ningún mensaje) y crea el passkey. Esa persona queda como la primera
administradora, con todas las herramientas. Desde entonces la consola ofrece
«Sign in with your passkey». Un passkey queda atado al nombre del sitio, y
WebAuthn no admite direcciones IP: abre la consola como `http://switchboard.localhost:PUERTO`
(el extremo local del túnel), nunca como `127.0.0.1`.

`switchboard.localhost` resuelve al equipo desde el navegador; no necesita un
registro DNS público ni cambiar el túnel SSH o el puerto del listener. Si otra
herramienta usa el DNS del sistema y no resuelve el nombre, puedes añadir
`127.0.0.1 switchboard.localhost` al archivo `hosts` de tu equipo. El navegador
permite las passkeys en este contexto local sin instalar un certificado TLS.

**Pasar desde localhost:** las passkeys están vinculadas al nombre y no se
copian al nuevo dominio. Inicia sesión en `http://localhost:PUERTO`, abre tu
menú y elige **Sign in on another browser**. El enlace ya usa
`switchboard.localhost`: ábrelo y crea una passkey para la misma cuenta.
Las passkeys anteriores siguen sirviendo en `localhost`. Si ya no puedes
usarlas, recurre a la recuperación del host, que sí reemplaza las anteriores.

Si pierdes el dispositivo, el host emite una recuperación:
`sudo switchboard console recover -email tu@correo`. Revoca los passkeys
anteriores de esa persona y crea uno nuevo con el enlace. Un segundo
`console enrol` se rechaza mientras exista un administrador. Los administradores
no son usuarios de aplicaciones: un passkey de visitante no abre la consola.

Desde la consola, **Administrators** permite invitar a otra persona por correo con
una parte de tus herramientas (nunca más de las que tú tienes), y deshabilitarla;
sus sesiones abiertas terminan al instante. La invitación sirve una vez durante
24 horas y la persona la abre como `switchboard.localhost` por su propio túnel SSH.

`console login` y el token de admin siguen disponibles como vía de emergencia
y automatización.

## Sesión del operador

`console login` pide un nombre que el host afirma; no verifica una identidad
civil ni crea una cuenta de visitante. La autoridad sigue siendo quien puede
acceder al socket administrativo del host.

**Permisos por herramienta.** El host decide qué puede cambiar cada sesión con
`-tools` (lista separada por comas de `applications`, `people`, `sites`,
`publishing`, `connectors`; sin `-tools` concede todas). Por ejemplo,
`console login -name Ana -tools people` abre una sesión que invita y edita
grupos pero recibe 403 al cambiar aplicaciones o sitios. Un nombre desconocido
se rechaza. La consola muestra en **Admin** qué incluye la sesión y explica una
página que no puede abrir. El token de admin y el socket del host siguen siendo
la credencial completa del host y no se limitan.

- El enlace sirve una vez durante cinco minutos. Su fragmento se elimina de
  la URL al abrirlo y se intercambia por una credencial de quince minutos.
- La credencial activa vive solo en la memoria de esa pestaña. Recargarla,
  cerrarla, bloquearla o reiniciar el proceso termina esa comodidad de acceso.
- `console list` muestra IDs, nombres y vencimientos; `console revoke -id ID`
  revoca un enlace pendiente o su sesión activa, conservando el mismo ID.
- Las mutaciones llevan actor `operator:<nombre>`. Se registran emisión,
  acceso, salida y revocación. Cuando la auditoría administrativa es obligatoria,
  sus intents deben persistirse antes de emitir o revocar.
- Una sesión de navegador no recupera cuentas, no configura la clave de una
  GitHub App y no enrola, rota o revoca conectores: esas acciones usan el socket.
- El token global sigue disponible por compatibilidad; **Lock** elimina su copia
  de esta pestaña, no revoca ese token en su archivo de origen.

Para una VPS, prepara un transporte privado, por ejemplo:

```sh
ssh -N -L 19090:127.0.0.1:9090 operador@tu-edge
# En la terminal del edge, con autoridad sobre su socket:
switchboard console login -config /etc/switchboard/config.json \
  -name Octavio -url http://127.0.0.1:19090
```

Abre el enlace en el navegador del equipo que tiene el forward. No publiques
el listener administrativo en Internet ni lo pongas como upstream de una
ruta pública. El bootstrap no convierte al primer visitante en dueño.

## Agregar una web desde Applications

**Add website** es un solo recorrido en la consola (`UI-006`, 28/10/2026):

1. **Dirección y origen.** El hostname (nuevo o el que mudas) y qué la sirve:
   una aplicación en ejecución (URL privada), archivos estáticos o una app detrás
   de un conector ya enrolado. Con archivos estáticos subes el `tar.gz` de
   `dist` en el mismo paso: el nombre del sitio sale de la dirección (se puede
   cambiar) y la primera publicación crea el sitio. Switchboard no compila.
2. **Quién entra.** Un grupo, cualquier persona autenticada o público. Por
   defecto se pide un grupo; varios grupos significan **todos**. Opcionalmente
   escribes correos para invitar: cada persona recibe un enlace de un solo uso
   para crear su passkey y queda en esos grupos.
3. **Revisión.** Muestra destino, acceso, invitaciones y certificado. Nada cambia
   hasta guardar; la confirmación vuelve a comprobar el digest de la config.
4. **Conectar.** Al guardar se publica el build, se guarda la ruta y se crean las
   invitaciones (sus enlaces se copian ahí, solo esa vez). El panel muestra el
   **único registro DNS** que falta y lo comprueba cada pocos segundos:
   DNS → certificado → una petición pública con un token de un solo uso que solo
   este edge responde (`/.well-known/switchboard-probe/…`, antes del pipeline).
   El mismo panel se abre en cada fila con el botón de globo.

Si la petición llega a otro servidor, el DNS todavía apunta al hosting anterior.
Si el proxy de Cloudflare responde 52x, no pudo hablar con el origen: revisa
Origin Pulls y el modo SSL. Prueba siempre una persona permitida y otra sin
permiso antes de mover un dominio en uso, y bloquea el origen antiguo: mientras
siga abierto, **todavía puede eludirse la nueva política**.

### Qué hace falta en la configuración

- **Certificados.** Con TLS por HTTP-01 (sin `tls.dns01`), cualquier ruta guardada
  obtiene su certificado en la primera visita, sin editar `tls.domains` ni
  reiniciar; el listener solo pide certificados para rutas configuradas. Con
  `tls.dns01` el certificado gestionado define los nombres posibles (un wildcard
  cubre sus subdominios); un hostname fuera de ellos se rechaza al guardar.
- **DNS.** `dns.target` es la dirección pública del edge: una IP (registro A o
  AAAA) o un hostname (CNAME). Sin ella el panel igual verifica, pero no puede
  escribir el registro exacto. `dns.proxied` marca el registro como proxied.
- **Cloudflare (opcional).** `dns.cloudflare.token` (`file:` o `env:`) con permiso
  *Zone · DNS · Edit* sobre las zonas servidas habilita **Create it in
  Cloudflare**. Nunca reemplaza un registro existente (puede ser el hosting
  vivo); si ya hay uno, hay que cambiarlo a mano. Sin token, el registro se
  copia y se crea en cualquier proveedor.

```json
"dns": { "target": "203.0.113.10", "proxied": true,
         "cloudflare": { "token": "file:/etc/switchboard/cloudflare-dns.token" } }
```

- **Origin Pulls a nivel de zona.** Detrás del proxy de Cloudflare, activa
  *Authenticated Origin Pulls* para toda la zona con el mismo certificado
  cliente (cuya CA está en `tls.origin_client_ca_file`). Así un hostname nuevo no
  necesita una asociación por hostname. Es un ajuste único en el panel de
  Cloudflare; el edge no cambia.
- **Apps privadas en otro dominio.** Con el login de passkeys, una app privada
  fuera de `auth.cookie_domain` recibe su propia sesión del host después del
  login central (un código de un solo uso, ver
  [sesiones por host](../architecture/host-sessions.md)). Con sólo OIDC
  externo sigue rechazándose al revisar.

La ficha reúne destino y acceso, enlace al simulador, publicación de artefactos
y acceso a versiones/rollback. **Configure** conserva campos avanzados cuando
se elige mantener el destino o la política. Cambiar expresamente la política
básica la reemplaza: revisa antes qué acceso se está quitando o dando.

## Dominio, rollback y backend

Para un proyecto Pages ya configurado, agregar un hostname con el mismo `site`
lo asocia al proyecto al guardar el snapshot. No requiere un token DNS de un
proveedor. El sistema no deja cambiar el último hostname de un proyecto a otro
destino y dejarlo huérfano. Los cambios permanecen tras reiniciar.

Si la nueva ruta era de prueba, puedes quitarla en Applications o restaurar la
configuración anterior y recargar. Si era un dominio de proyecto, la asociación se retira de forma atómica al quitar la ruta; si es el último
dominio, desconecta primero su publicador en CI projects. Las referencias se validan.
Un rollback de Pages activa un artefacto retenido; **no** revierte DNS, una base
de datos ni un cambio del backend.

Switchboard no inicia ni instala tu aplicación. Si ya tiene login, conserva
ese login hasta elegir y probar un contrato de [integración](../architecture/identity-integration.md).
No deshabilites permisos internos para ahorrar una pantalla.

## Personas, grupos y sesiones

People permite asignar grupos al invitar y editar la membresía nativa con
revisión de consecuencias. Actualiza solo desde una lectura vigente; un cambio
concurrente devuelve conflicto. Al cambiar grupos se borran sus sesiones nativas
en la misma transacción, y cada petición consulta los grupos actuales para
cubrir un login concurrente con la baja. Reinvitar no cambia membresías.
El catálogo de grupos de la API (`GET /_/api/groups`) reúne membresías locales y
requisitos de rutas; los grupos de OIDC se administran en su proveedor.

En `/_/auth/me`, el visitante ve las aplicaciones que puede abrir y sus propias
sesiones, con fecha de uso/vencimiento y método. Puede cerrar una o todas; no ve
ni revoca las de otra persona. Las fechas no son postura ni ubicación del equipo.
La lista de dispositivos/passkeys sigue en `/_/auth/devices` y pide prueba reciente.

## Actualizar una instalación anterior

La migración **012** añade metadatos de caducidad de conectores y un índice de
sesiones. También termina las sesiones OIDC anteriores: sus `sub` crudos no se
pueden convertir con seguridad a sujetos calificados por emisor. Los nuevos
sujetos OIDC codifican emisor y `sub`; no se vinculan cuentas por correo.
Las cuentas y passkeys nativas se conservan. Las sesiones nativas con método
`passkey` siguen vigentes; una sesión antigua sin método registrado requiere
un nuevo login.

Usa el procedimiento de [upgrade y backup](systemd.md) y aplica migraciones de
forma explícita. No uses una versión nueva contra una base sin migrar. Una
reversión del binario tras migrar necesita además la copia previa de la base.

## Aceptación reproducible

Con Node, `agent-browser` y Chromium disponibles:

```sh
scripts/build-console.sh
go build -o /tmp/switchboard-product-candidate ./cmd/switchboard
SWITCHBOARD_BIN=/tmp/switchboard-product-candidate node scripts/demo/product-acceptance.mjs
```

El script inicia un edge local con datos ficticios y socket propio; comprueba
login temporal, limpieza del fragmento, revisión/alta de app, cancelación y cambio
de membresía, estados de GitHub/conectores y tamaños móviles. No usa una instancia
real ni imprime credenciales. Conserva capturas y elimina su estado descartable.
La aceptación de una persona externa y un equipo distinto se registra aparte.

Las passkeys de administradores tienen su propia prueba, con Playwright y un
autenticador virtual de Chrome por dispositivo (ninguna credencial sale de la
corrida):

```sh
SWITCHBOARD_BIN=/tmp/switchboard-product-candidate CHROME=/ruta/a/chrome \
  node scripts/demo/passkey-acceptance.mjs
```

Comprueba que el enlace del primer administrador funciona una sola vez, que se
entra con passkey después de salir, que el enlace para otro navegador agrega una
passkey y no sirve dos veces, que borrar desde "Your passkeys" la passkey de un
dispositivo perdido cierra su sesión y la deja inútil (también desde el menú del
celular), que una invitación con una sola herramienta da sólo
esa, y que el reseteo de otro administrador corta su sesión abierta y deja inútil
su passkey vieja. No prueba el vencimiento por inactividad (30 minutos) ni Firefox.

Las apps privadas en otro dominio (sesiones por host, `ID-015`) tienen la suya,
con dos apps en sitios distintos del login (`*.other.localhost`,
`*.third.localhost`):

```sh
SWITCHBOARD_BIN=/tmp/switchboard-product-candidate CHROME=/ruta/a/chrome \
  node scripts/demo/host-sessions-acceptance.mjs
```

Comprueba que con la sesión central abierta la app se abre sin pedir nada y en
la ruta pedida, que su cookie es sólo de ese host y distinta de la central, que
ninguna cookie de sesión llega al upstream, que la política de la app sigue
decidiendo (403), que un código capturado no sirve en otro navegador y queda
quemado, que entrar desde la app vuelve a ella, y que salir desde la app o desde
el login cierra todo el login. Corre por HTTP en `localhost`: no prueba HTTPS,
dominios reales ni Safari/Firefox.
