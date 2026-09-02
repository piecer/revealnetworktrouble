FROM nginx:1.27-alpine
RUN rm -rf /usr/share/nginx/html/*
COPY nginx.conf /etc/nginx/nginx.conf
COPY index.html styles.css app.js state.js topology-model.js topology-renderer.js /usr/share/nginx/html/
EXPOSE 80
