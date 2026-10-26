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
switchboard console login -config /ruta/my-edge/config.json -name Octavio
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
sube un tar.gz del contenido de `dist` en **Applications → Publish an artifact**,
o usa `site deploy` con el token mediante su referencia a archivo. La web del
proyecto se construye fuera del edge.

En Windows no hay socket Unix administrativo; las sesiones del operador
emitidas por el host requieren Linux/macOS. Para la consola local Windows se
conserva el token por archivo y la recuperación offline con el proceso detenido.
La instalación de servicio descrita en este repositorio es Linux/systemd.

## Sesión del operador

`console login` pide un nombre que el host afirma; no verifica una identidad
civil ni crea una cuenta de visitante. La autoridad sigue siendo quien puede
acceder al socket administrativo del host.

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

## Incorporar una web existente desde Applications

1. Lee la instalación con la sesión del operador y pulsa **Add existing website**.
2. Usa primero un hostname temporal. Elige aplicación en ejecución, artefacto
   estático o aplicación mediante un conector ya enrolado.
3. Elige acceso explícito: un grupo, cualquier persona autenticada o público.
   Por defecto se pide un grupo. Varios grupos requeridos significan **todos**,
   no cualquiera de ellos. Sin identidad configurada no puede guardarse una
   política privada que dependa de ella.
4. Revisa el plan. No modifica DNS ni el runtime; indica cobertura TLS configurada
   y comprobaciones pendientes. Un listener con TLS se niega a guardar un host
   que no esté cubierto por `tls.domains`. Esa cobertura no prueba que el
   certificado ya esté emitido o servido. Con TLS externo, su verificación queda
   declarada pendiente.
5. Confirma las consecuencias y guarda. El servidor vuelve a comprobar el digest
   de configuración bajo la misma exclusión que la mutación. Si alguien cambió
   la config, refresca y revisa; no se sobreescribe silenciosamente.
6. Prueba contenido, recursos, callbacks de login, cargas y WebSockets que use
   la app. Prueba una persona permitida y otra sin permiso; busca sus decisiones
   en Access log y reproduce la regla en el simulador.
7. Solo después prepara el dominio final y cambia DNS manualmente. Conserva
   hosting y registros anteriores durante la ventana de reversión. Si el origen
   antiguo sigue abierto, **todavía puede eludirse la nueva política**: bloquéalo
   antes de considerar privada la aplicación.

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
