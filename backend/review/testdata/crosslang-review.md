📈 CALIDAD: Fiabilidad=B Seguridad=C Mantenibilidad=B
🚦 Quality Gate: FAILED

El cambio introduce una condición de carrera y un secreto en el repositorio.

### 🔴 [Blocker · Bug] race-condition · F-001

El contador se lee sin lock

📍 Ubicación: src/app.ts:12-14
💭 Por qué: dos goroutines escriben el mismo campo sin sincronización
💡 Sugerencia: proteger el acceso con un mutex
🎯 Confianza: 90

### 🚨 [Mayor · Security Hotspot] hardcoded-secret · F-002

La clave viaja en el código

📍 Ubicación: src/config.ts:5
💭 Por qué: un secreto en el repositorio es un secreto filtrado
🎯 Confianza: 75

### 🔵 [Info · Code Smell] naming · F-003

El nombre no dice qué hace

📍 Ubicación: `src/util.ts`
💭 Por qué: `doIt` no describe la operación

## 👍 Lo que está bien

- La cobertura sube del 30 al 80
- 🎯 Confianza: 99 — este número está dentro de una viñeta y no pertenece a ningún hallazgo

## 🗒️ Notas

- El cambio no toca la migración pendiente
