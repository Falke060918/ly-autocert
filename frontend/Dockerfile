# syntax=docker/dockerfile:1
# 멀티스테이지 빌드 — Next.js standalone output.
# 전제: 프론트 3000 포트, 백엔드 API 8080 포트.
# 브라우저는 같은 출처(/api/*)만 호출하고 Next 서버가 백엔드로 프록시한다.
# 프록시 대상(BACKEND_INTERNAL_URL)은 rewrites 설정에 빌드 시점 고정되므로 build-arg로 넘긴다.
#   docker build --build-arg BACKEND_INTERNAL_URL=http://backend:8080 .

FROM node:24-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:24-alpine AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ARG BACKEND_INTERNAL_URL=http://localhost:8080
ENV BACKEND_INTERNAL_URL=${BACKEND_INTERNAL_URL}
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:24-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV HOSTNAME=0.0.0.0
ENV PORT=3000
RUN addgroup -S -g 1001 nodejs && adduser -S -u 1001 nextjs
COPY --from=builder /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static
USER nextjs
EXPOSE 3000
CMD ["node", "server.js"]
