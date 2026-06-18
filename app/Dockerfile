FROM nginx:1.27-alpine

COPY ./nginx.conf /etc/nginx/nginx.conf
COPY ./html/index.html /usr/share/nginx/html/index.html
COPY ./html/vite.svg /usr/share/nginx/html/vite.svg
RUN mkdir -p /usr/share/nginx/html/assets
COPY ./html/assets/* /usr/share/nginx/html/assets/

EXPOSE 80
