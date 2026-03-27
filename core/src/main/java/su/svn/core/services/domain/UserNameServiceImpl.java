/*
 * This file was last modified at 2026.03.27 14:01 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * UserNameServiceImpl.java
 * $Id$
 */

package su.svn.core.services.domain;

import lombok.RequiredArgsConstructor;
import lombok.experimental.FieldDefaults;
import lombok.extern.slf4j.Slf4j;
import org.springframework.data.crossstore.ChangeSetPersister;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;
import su.svn.core.domain.entities.UserName;
import su.svn.core.models.dto.NewUserName;
import su.svn.core.repository.UserNameRepository;
import su.svn.core.services.mappers.UserNameMapper;

import static lombok.AccessLevel.PRIVATE;

/**
 * Implementation of {@link UserNameService}.
 *
 * <p>Handles persistence and validation logic for user names.</p>
 */
@FieldDefaults(level = PRIVATE, makeFinal = true)
@RequiredArgsConstructor
@Service
@Slf4j
public class UserNameServiceImpl implements UserNameService {

    UserNameRepository userNameRepository;
    UserNameMapper userNameMapper;

    @Override
    public UserName findByUserName(String userName) throws ChangeSetPersister.NotFoundException {
        return userNameRepository.findByUserName(userName)
                .orElseThrow(ChangeSetPersister.NotFoundException::new);
    }

    @Override
    @Transactional
    public UserName save(NewUserName newUserName) {
        return userNameRepository.save(userNameMapper.toEntity(newUserName));
    }
}
