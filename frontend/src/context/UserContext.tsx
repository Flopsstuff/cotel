import { createContext, useContext, useState, type ReactNode } from 'react'

interface UserContextValue {
  userId: string
  setUserId: (id: string) => void
}

const UserContext = createContext<UserContextValue>({ userId: '', setUserId: () => {} })

export function UserProvider({ children }: { children: ReactNode }) {
  const [userId, setUserId] = useState('')
  return (
    <UserContext.Provider value={{ userId, setUserId }}>
      {children}
    </UserContext.Provider>
  )
}

export function useUserContext() {
  return useContext(UserContext)
}
