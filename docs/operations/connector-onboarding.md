# Incorporar un host con conector

`TUN-004`, implementación local 26/10/2026. Usa la confianza del host y la CA
privada existente; no crea un servicio de PKI general ni administra procesos.
La consola Connectors muestra alcance, conexión, caducidad conocida y pasos.

1. En el host que ejecutará el conector:

```sh
switchboard connector prepare -dir ./connector-identity -name homelab
```

Se crea un directorio nuevo privado con `connector.key` y `connector.csr`.
No se sobrescribe una identidad existente. **Solo el CSR público viaja**.
La clave privada permanece en su host.

2. Transfiere `connector.csr` al host autorizado que ya tiene la CA:

```sh
switchboard connector sign -csr ./connector.csr \
  -ca-dir /ruta/ca-existente -out ./connector.crt
```

El signer existente verifica la firma del CSR y emite únicamente el rol de
conector, con EKU cliente y nombre fijo `connector.homelab.internal`, por
30 días. La identidad de autorización del registro es la huella de la hoja,
no un nombre elegido libremente. No genera roles de servidor ni reemplaza
certificados existentes. Una CA próxima a vencer requiere rollover planificado.

3. Devuelve el certificado público a su carpeta de origen y distribuye el
certificado público de la CA por un canal autenticado. En el conector:

```sh
switchboard connector check -dir ./connector-identity -ca ./ca.crt
```

Comprueba par clave/certificado, cadena, rol, nombre y vida restante. Eso no
lo enrola ni le permite elegir cualquier aplicación.

4. En el edge, con acceso al socket administrativo del host:

```sh
switchboard connector enroll -config /etc/switchboard/config.json \
  -name homelab -cert ./connector.crt
```

La API acepta el certificado público y registra su huella y caducidad. Para
registros antiguos o enrolados solo por huella, la caducidad es **desconocida**;
no se inventa. La rotación pública por API registra el vencimiento nuevo.
La revocación y validación mTLS conservan la autoridad, no esos metadatos.

5. Configura los mapeos explícitos del conector con `tunnel connect` según
[transporte persistente](../architecture/tunnel-persistent.md). Solo puede servir
los hosts/destinos declarados. Da de alta la ruta en Applications y prueba una
persona permitida y otra sin acceso; después prueba caída/reconexión/revocación.

La emisión es por 30 días. Integra el host en el procedimiento existente de
[renovación](../architecture/tunnel-renewal.md) o planifica y prueba su rotación
antes del vencimiento. La automatización actual del piloto no se convierte
automáticamente en un renovador de cualquier flota por crear otro directorio.
No hay una afirmación de segundo host real aceptado por este trabajo local.

Para retirar el host, revisa sus aplicaciones en Connectors y ejecuta
`connector revoke -config ... -name homelab` en el edge. La conexión se cierra
cuando el edge revalida, normalmente dentro de cinco segundos; un reconector
con esa identidad se rechaza. Luego detén su proceso en el propio host. No borres
la CA compartida ni los datos de las aplicaciones para revocar un conector.
