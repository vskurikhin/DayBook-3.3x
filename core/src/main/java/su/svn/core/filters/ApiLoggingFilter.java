/*
 * This file was last modified at 2026.05.07 14:57 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * ApiLoggingFilter.java
 * $Id$
 */

package su.svn.core.filters;

import jakarta.servlet.Filter;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletRequest;
import jakarta.servlet.ServletResponse;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import lombok.extern.slf4j.Slf4j;
import org.slf4j.MDC;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.core.userdetails.User;
import su.svn.core.domain.entities.UserName;
import su.svn.core.servlet.GzipCountingResponseWrapper;

import java.util.HashMap;
import java.util.Map;
import java.util.UUID;

import static su.svn.lib.Constants.REQUEST_ID;

@Slf4j
public class ApiLoggingFilter implements Filter {

    public static final String GUEST = "guest";

    private final String requestIdParamName;

    public ApiLoggingFilter(String requestIdParamName) {
        this.requestIdParamName = requestIdParamName.toLowerCase();
    }

    @SuppressWarnings("StringBufferReplaceableByString")
    @Override
    public void doFilter(ServletRequest request, ServletResponse response, FilterChain chain) {
        try {
            var httpServletRequest = (HttpServletRequest) request;
            var httpServletResponse = (HttpServletResponse) response;

            var headerMap = this.getTypeSafeHeaderMap(httpServletRequest);
            var requestMap = this.getTypeSafeRequestMap(httpServletRequest);
            var requestId = headerMap.containsKey(requestIdParamName)
                    ? headerMap.get(requestIdParamName)
                    : UUID.randomUUID().toString();
            MDC.put(REQUEST_ID, requestId);
            final StringBuilder logRequest = new StringBuilder("HTTP ").append(httpServletRequest.getMethod())
                    .append(" \"").append(httpServletRequest.getServletPath()).append("\"")
                    .append(", user_name=").append(getUserName())
                    .append(", parameters=").append(requestMap)
                    .append(", remote_address=").append(httpServletRequest.getRemoteAddr());
            log.info(logRequest.toString());
            var wrapped = new GzipCountingResponseWrapper(httpServletResponse);
            try {
                chain.doFilter(httpServletRequest, wrapped);
            } finally {
                log.info(
                        "HTTP RESPONSE, status={}, content_type={}, raw_size={}, gzip_size={}",
                        wrapped.getStatus(), wrapped.getContentType(), wrapped.getRawSize(), wrapped.getGzipSize()
                );
                MDC.clear();
            }
        } catch (Throwable a) {
            log.error(a.getMessage());
        }
    }

    private Map<String, String> getTypeSafeHeaderMap(HttpServletRequest request) {
        var typeSafeRequestMap = new HashMap<String, String>();
        var requestHeaderNames = request.getHeaderNames();
        while (requestHeaderNames.hasMoreElements()) {
            var requestHeaderName = requestHeaderNames.nextElement();
            String requestHeaderValue;
            if (requestHeaderName.equalsIgnoreCase("authorization")) {
                requestHeaderValue = "********";
            } else {
                requestHeaderValue = request.getHeader(requestHeaderName);
            }
            typeSafeRequestMap.put(requestHeaderName.toLowerCase(), requestHeaderValue);
        }
        return typeSafeRequestMap;
    }

    private Map<String, String> getTypeSafeRequestMap(HttpServletRequest request) {
        var typeSafeRequestMap = new HashMap<String, String>();
        var requestParamNames = request.getParameterNames();
        while (requestParamNames.hasMoreElements()) {
            var requestParamName = requestParamNames.nextElement();
            String requestParamValue;
            if (requestParamName.equalsIgnoreCase("password")) {
                requestParamValue = "********";
            } else {
                requestParamValue = request.getParameter(requestParamName);
            }
            typeSafeRequestMap.put(requestParamName, requestParamValue);
        }
        return typeSafeRequestMap;
    }

    private static String getUserName() {
        Object principal = SecurityContextHolder.getContext().getAuthentication().getPrincipal();
        return switch (principal) {
            case UserName userName -> userName.userName();
            case User user -> user.getUsername();
            default -> GUEST;
        };
    }
}