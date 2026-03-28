import * as React from 'react';
import PropTypes from 'prop-types';

import { STATUS } from '../utils/utils';

const initialState = {
  user: {},
  token: null,
  expires_at: null,
  isAuthenticated: false,
  status: STATUS.IDLE,
};

const AuthContext = React.createContext({
  ...initialState,
  login: (user = {}, token = '', expires_at = '') => {},
  logout: () => {},
  updateUser: () => {},
  setAuthenticationStatus: () => {},
});

const authReducer = (state, action) => {
console.log("authReducer() state: " + state + " action: " + action)
  switch (action.type) {
    case 'login': {
      return {
        user: action.payload.data.user,
        token: action.payload.data.token,
        expires_at: action.payload.data.expires_at,
        isAuthenticated: true,
        verifyingToken: false,
        status: STATUS.SUCCEEDED,
      };
    }
    case 'logout': {
      return {
        ...initialState,
        status: STATUS.IDLE,
      };
    }
    case 'updateUser': {
      return {
        ...state,
        user: action.payload.data.user,
      };
    }
    case 'status': {
      return {
        ...state,
        status: action.payload.data.status,
      };
    }
    default: {
      throw new Error(`Unhandled action type: ${action.type}`);
    }
  }
};

const AuthProvider = ({ children }) => {
  const [state, dispatch] = React.useReducer(authReducer, initialState);

  const login = React.useCallback((user, token, expires_at) => {
    dispatch({
      type: 'login',
      payload: {
        data: {
          user,
          token,
          expires_at,
        },
      },
    });
  }, []);

  const logout = React.useCallback(() => {
    dispatch({
      type: 'logout',
    });
  }, []);

  const updateUser = React.useCallback((user) => {
    dispatch({
      type: 'updateUser',
      payload: {
        data: {
          user,
        },
      },
    });
  }, []);

  const setAuthenticationStatus = React.useCallback((status) => {
      console.log("setAuthenticationStatus: " + status)
    dispatch({
      type: 'status',
      payload: {
        data: {
          status,
        },
      },
    });
  }, []);

  const value = React.useMemo(
    () => ({ ...state, login, logout, updateUser, setAuthenticationStatus }),
    [state, setAuthenticationStatus, login, logout, updateUser]
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
};

const useAuth = () => {
  const context = React.useContext(AuthContext);

  if (context === undefined) {
    throw new Error('useAuth must be used within a AuthProvider');
  }

  return context;
};

AuthProvider.propTypes = {
  children: PropTypes.element.isRequired,
};

export { AuthProvider, useAuth };