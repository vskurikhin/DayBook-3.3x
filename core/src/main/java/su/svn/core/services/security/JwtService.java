/*
 * This file was last modified at 2026.04.23 20:14 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * JwtService.java
 * $Id$
 */

package su.svn.core.services.security;

import su.svn.core.domain.entities.UserName;

import java.util.Set;

public interface JwtService {
    String extractUserName(String token);

    Set<String> extractGroups(String token);

    boolean isTokenValid(String token);

    boolean isTokenValid(String token, String upn);
}