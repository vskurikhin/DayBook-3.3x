/*
 * This file was last modified at 2026.04.15 20:40 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * UserHasRoles.java
 * $Id$
 */

package su.svn.api.model.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import lombok.Builder;

import java.io.Serializable;
import java.util.Set;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record UserHasRoles(
        @JsonProperty("user_name") String userName,
        @JsonProperty Set<String> roles,
        @JsonProperty Integer flags,
        @JsonProperty Boolean visible) implements Serializable {
    @Builder
    public UserHasRoles {
    }
}
