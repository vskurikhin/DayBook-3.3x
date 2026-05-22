/*
 * This file was last modified at 2026.05.22 19:39 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * MarkdownRecordMapper.java
 * $Id$
 */

package su.svn.api.services.mappers;

import org.mapstruct.Mapper;
import org.mapstruct.Mapping;
import org.mapstruct.MappingConstants;
import org.mapstruct.ValueMapping;
import su.svn.api.domain.entities.PostRecord;
import su.svn.api.models.dto.EntityModelResourceRecordView;
import su.svn.api.models.dto.ResourceMarkdownRecord;
import su.svn.api.models.dto.UpdateMarkdownRecord;

/**
 * MapStruct mapper for converting markdown record DTOs
 * and {@link PostRecord} entities.
 *
 * <p>
 * Provides mapping operations for transforming
 * update DTOs into entities and entities into
 * API resource DTOs.
 * </p>
 */
@Mapper(componentModel = "cdi")
public interface MarkdownRecordMapper extends DateTimeMapper {

    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    @Mapping(target = "parent", ignore = true)
    @Mapping(target = "sequenceId", ignore = true)
    @Mapping(target = "userName", constant = "root")
    @Mapping(target = "createTime", ignore = true)
    @Mapping(target = "updateTime", ignore = true)
    @Mapping(target = "enabled", ignore = true)
    @Mapping(target = "localChange", ignore = true)
    @Mapping(target = "blob", ignore = true)
    @Mapping(target = "json", ignore = true)
    @Mapping(target = "texts", ignore = true)
    @Mapping(target = "fileName", ignore = true)
    @Mapping(target = "html", ignore = true)
    @Mapping(target = "link", ignore = true)
    @Mapping(target = "value", ignore = true)
    PostRecord toEntity(EntityModelResourceRecordView recordView);

    /**
     * Converts an update DTO into a {@link PostRecord} entity.
     *
     * @param record update DTO
     * @return mapped entity
     */
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    @Mapping(target = "parent", ignore = true)
    @Mapping(target = "sequenceId", ignore = true)
    @Mapping(target = "userName", constant = "root")
    @Mapping(target = "createTime", ignore = true)
    @Mapping(target = "updateTime", ignore = true)
    @Mapping(target = "lastChangedTime", ignore = true)
    @Mapping(target = "enabled", ignore = true)
    @Mapping(target = "localChange", ignore = true)
    @Mapping(target = "blob", ignore = true)
    @Mapping(target = "json", ignore = true)
    @Mapping(target = "texts", ignore = true)
    @Mapping(target = "fileName", ignore = true)
    @Mapping(target = "html", ignore = true)
    @Mapping(target = "link", ignore = true)
    @Mapping(target = "value", ignore = true)
    PostRecord toEntity(UpdateMarkdownRecord record);

    /**
     * Converts a {@link PostRecord} entity into a resource DTO.
     *
     * @param postRecord source entity
     * @return mapped resource DTO
     */
    @ValueMapping(source = "UNRECOGNIZED", target = MappingConstants.NULL)
    ResourceMarkdownRecord toResource(PostRecord postRecord);
}
