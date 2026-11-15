import { createContext, useContext, useState, type ReactNode } from 'react'

interface ActorContextValue {
  actorId: string
  setActorId: (id: string) => void
}

const ActorContext = createContext<ActorContextValue>({ actorId: '', setActorId: () => {} })

export function ActorProvider({ children }: { children: ReactNode }) {
  const [actorId, setActorId] = useState('')
  return (
    <ActorContext.Provider value={{ actorId, setActorId }}>
      {children}
    </ActorContext.Provider>
  )
}

export function useActorContext() {
  return useContext(ActorContext)
}
