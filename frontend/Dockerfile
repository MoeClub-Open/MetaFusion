# syntax=docker/dockerfile:1
# ==============================================================================
# MetaFusion Frontend - Next.js standalone on Bun 1.4 (Fast BuildKit Cache)
# ==============================================================================
FROM oven/bun:1.4.3-alpine AS base

FROM base AS deps
WORKDIR /app
# 按提交的 bun.lock 安装；锁文件缺失或漂移时构建失败。
COPY package.json bun.lock ./
RUN --mount=type=cache,target=/root/.bun \
    bun install --frozen-lockfile

FROM base AS builder
WORKDIR /app
ARG BACKEND_ORIGIN=http://backend:8080
ENV BACKEND_ORIGIN=$BACKEND_ORIGIN
# NEXT_PUBLIC_* 会被 Next 在**构建期**内联进产物，因此只能走 build arg（运行期 env 无效）。
# 留空 = 各服务的页面入口退回同源路径（现状）；服务自带 UI（审计 D1）后在编排里传值。
ARG NEXT_PUBLIC_RESOURCE_STATION_URL=
ARG NEXT_PUBLIC_DOCS_URL=
ENV NEXT_PUBLIC_RESOURCE_STATION_URL=$NEXT_PUBLIC_RESOURCE_STATION_URL
ENV NEXT_PUBLIC_DOCS_URL=$NEXT_PUBLIC_DOCS_URL
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED 1
# Bun 1.4 原生跑 Next 构建（比 Node 快 3-5 倍）
RUN --mount=type=cache,target=/app/.next/cache \
    bun run build

FROM base AS runner
WORKDIR /app
ENV NODE_ENV production
ENV NEXT_TELEMETRY_DISABLED 1
RUN addgroup --system --gid 1001 nodejs && adduser --system --uid 1001 nextjs
COPY --from=builder /app/public ./public
RUN mkdir -p .next && chown nextjs:nodejs .next
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static
USER nextjs
EXPOSE 3000
ENV PORT 3000
ENV HOSTNAME "0.0.0.0"
# Next standalone 的 server.js 为 Node 产物，Bun 可直接以 Node 兼容模式运行
CMD ["bun", "server.js"]
