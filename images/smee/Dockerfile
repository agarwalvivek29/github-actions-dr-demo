# Relays GitHub App webhooks from a smee.io channel into the cluster (no public ingress needed).
FROM node:22-alpine
RUN npm install -g smee-client@4 && npm cache clean --force
USER node
ENTRYPOINT ["sh", "-c", "exec smee --url \"$SMEE_URL\" --target \"$TARGET\""]
