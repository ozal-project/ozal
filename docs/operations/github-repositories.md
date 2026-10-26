# Repositorios GitHub y confianza de publicación

`PAGES-010`, implementación local 26/10/2026. Es opcional. Subir un artefacto
manual no requiere GitHub ni una cuenta central de Switchboard.

## Conectar una instalación propia

1. Registra una GitHub App propia, con permiso de repositorio **Metadata: read**.
   No necesita escribir contenido/workflows, leer secretos ni importar miembros.
   Deshabilita webhooks si no los necesitas: este adaptador consulta metadatos.
2. Instálala solamente en los repositorios que deseas ver. Conserva su App ID
   y el ID de instalación. Una instalación suspendida, revocada o sin acceso
   al repo deja de poder descubrirlo o crear nueva confianza desde la consola.
3. Genera su clave privada en GitHub y guárdala **localmente** en el host, con
   permisos para el servicio y referencia `file:`/`env:`. No la pegues en la
   consola web, el chat, un argumento de clave literal o el repositorio.
4. En el host, con acceso al socket administrativo:

```sh
switchboard github connect -config /etc/switchboard/config.json \
  -app-id 123 -installation-id 456 -key file:/etc/switchboard/github-app-key
```

El comando guarda únicamente la referencia. No envía esa clave al navegador ni
resuelve y reescribe su contenido en config. La autenticación API usa un JWT
RS256 corto y un token de instalación reducido a metadatos, ambos en memoria.
El adaptador usa HTTPS a `api.github.com`, rechaza redirects y limita tiempos,
paginación y tamaño de respuesta. Errores externos se presentan sin contenido
crudo ni credenciales.

La instalación y los permisos pertenecen al operador. No hay una GitHub App
central de Switchboard ni una nueva dependencia de login SaaS para visitantes.
La prueba con GitHub real requiere configurar esta App; los tests locales usan
un servidor simulado y claves generadas para esa prueba.

## Elegir quién publica

En **CI projects**, lee repositorios y proyectos. Crea primero un sitio estático
en Applications si no existe. Selecciona un repo, sitio, nombre de proyecto,
ref completa (`refs/heads/main`) y archivo de workflow (`deploy.yml`).

Al confirmar, el servidor vuelve a consultar la instalación, comprueba que el
repo sigue disponible y usa sus **IDs de repo y propietario**, no nombres
aportados por el navegador. Revisa también ref y workflow. Reemplazar un proyecto
reemplaza su confianza anterior, incluidos tokens de despliegue. La operación
se niega ante una config desactualizada. No cambia archivos en GitHub.

El endpoint de deploy debe tener un hostname HTTPS dedicado, preparado por el
operador. Su activación afecta listeners y requiere el procedimiento del host.
Luego incorpora el workflow y scripts de [publicación](../architecture/deploy-product.md)
al repo del sitio, con `contents: read`, `id-token: write` solo en el job de
deploy y acciones fijadas a SHA. La ref/archivo deben coincidir con la confianza.
El build corre en Actions; Switchboard recibe el artefacto terminado.

Las variables públicas de build pueden terminar en JS descargable. Los secretos
de build viven en GitHub y no deben llegar a `dist`; los secretos de runtime
permanecen con la aplicación, en su host.

## Desconectar sin borrar contenido

**Disconnect publisher** elimina el proyecto y su confianza para futuras
peticiones de deploy; conserva rutas, archivos y publicaciones. Un upload que
ya fue autenticado puede terminar con su snapshot capturado: revisa Activity.

`switchboard github disconnect -config ...` desconecta solamente la exploración
API. **No revoca las reglas OIDC de publicación ya guardadas.** El propio comando
lo explica; elimina los publicadores por proyecto si también quieres detener CI.
Desinstalar la App en GitHub tampoco deshace por sí solo esas reglas del edge.

El OIDC de Actions identifica un workflow. No equivale a “entrar con GitHub”
como persona, no importa equipos/grupos y no da permisos de visitante.

Referencias verificadas: [autenticación de instalaciones](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation),
[API de instalaciones](https://docs.github.com/en/rest/apps/installations),
[permisos de Apps](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app).
